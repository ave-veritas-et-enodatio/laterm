// laterm — Claude Code sidecar that renders LaTeX math from the conversation
// log as inline terminal images. (Rust implementation; the validated Go
// prototype lives in prototype/.)

mod convo;
mod graphics;
mod logging;
mod mathscan;
mod render;
mod sixel;
mod termbg;
mod watch;

use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::{atomic::AtomicBool, Arc, Mutex};
use std::time::{Duration, Instant};

use chrono::{DateTime, Utc};
use ratex_types::color::Color;

const POLL_INTERVAL: Duration = Duration::from_millis(500);
const BG_QUERY_TIMEOUT: Duration = Duration::from_millis(200);
const DEFAULT_CATCH_UP_MINUTES: i64 = 5;

const USAGE: &str = "\
usage: laterm [options]

Renders LaTeX math from the current Claude Code conversation log as inline
terminal images.

options:
  --log <PATH>          write diagnostics to PATH (default: no logging)
  --catch-up[=<MINS>]   render math from the last MINS minutes of history
                        before tailing (bare flag = 5 minutes)
  --version, -V         print version and exit
  --help                show this help and exit
";

struct Args {
    log: Option<PathBuf>,
    catch_up: Option<i64>,
}

/// The outcome of parsing argv: run with the given args, show help, or show
/// version. Help and version are distinct terminal actions, both exiting 0.
enum Invocation {
    Run(Args),
    Help,
    Version,
}

/// Parse argv (excluding the program name). Returns the [`Invocation`] to
/// perform, or Err with a message on a malformed argument.
fn parse_args(argv: impl IntoIterator<Item = String>) -> Result<Invocation, String> {
    let mut args = Args {
        log: None,
        catch_up: None,
    };
    let mut it = argv.into_iter();

    while let Some(arg) = it.next() {
        if arg == "--help" || arg == "-h" {
            return Ok(Invocation::Help);
        } else if arg == "--version" || arg == "-V" {
            return Ok(Invocation::Version);
        } else if arg == "--log" {
            let path = it.next().ok_or("--log requires a path argument")?;
            args.log = Some(PathBuf::from(path));
        } else if let Some(path) = arg.strip_prefix("--log=") {
            if path.is_empty() {
                return Err("--log requires a path argument".to_string());
            }
            args.log = Some(PathBuf::from(path));
        } else if arg == "--catch-up" {
            args.catch_up = Some(DEFAULT_CATCH_UP_MINUTES);
        } else if let Some(mins) = arg.strip_prefix("--catch-up=") {
            let n: i64 = mins
                .parse()
                .map_err(|_| format!("invalid --catch-up minutes: {mins}"))?;
            if n < 0 {
                return Err(format!("--catch-up minutes must be non-negative: {n}"));
            }
            args.catch_up = Some(n);
        } else {
            return Err(format!("unknown argument: {arg}"));
        }
    }

    Ok(Invocation::Run(args))
}

fn main() {
    std::process::exit(run());
}

fn run() -> i32 {
    let args = match parse_args(std::env::args().skip(1)) {
        Ok(Invocation::Run(a)) => a,
        Ok(Invocation::Help) => {
            print!("{USAGE}");
            return 0;
        }
        Ok(Invocation::Version) => {
            println!("laterm {}", env!("CARGO_PKG_VERSION"));
            return 0;
        }
        Err(e) => {
            eprint!("laterm: {e}\n\n{USAGE}");
            return 2;
        }
    };

    // No logging by default — opt in with `--log <PATH>`. Avoids multiple
    // instances clobbering one shared default file; enable it for diagnostics.
    if let Some(path) = &args.log {
        logging::init(path, logging::level_from_env());
    }

    // Select graphics protocol first — hard gate.
    let proto = match graphics::select() {
        Some(p) => p,
        None => {
            eprintln!("laterm: terminal supports neither the kitty graphics nor the iTerm2 (imgcat) protocol; run inside a compatible terminal (kitty, ghostty, iTerm2, WezTerm)");
            return 1;
        }
    };
    logging::info(&format!("graphics protocol: {}", proto.name));

    // Detect terminal background and configure render theme. SIXEL transparency
    // is unreliable across terminals, so it renders on an opaque background.
    let opaque_bg = proto.name == "sixel";
    apply_theme(opaque_bg);
    if opaque_bg {
        sixel::detect_cell_height(BG_QUERY_TIMEOUT);
    }

    // Derive log directory.
    let dir = match project_log_dir() {
        Ok(d) => d,
        Err(e) => {
            eprintln!("laterm: {e}");
            return 1;
        }
    };
    logging::info(&format!("watching {}", dir.display()));

    // Shutdown flag shared between threads.
    let shutdown = Arc::new(AtomicBool::new(false));

    // Clean shutdown on SIGINT/SIGTERM/SIGHUP (ctrlc "termination" feature;
    // Ctrl-C / CTRL_CLOSE on Windows) — cross-platform.
    let sd = shutdown.clone();
    if let Err(e) = ctrlc::set_handler(move || {
        sd.store(true, std::sync::atomic::Ordering::Relaxed);
    }) {
        eprintln!("laterm: signal setup failed: {e}");
        return 1;
    }

    // Reference X height (one render of "X"); block threshold is 1.5× of it.
    let ref_height = reference_height();
    let block_threshold = ref_height * 3 / 2;

    // Stdout lock shared between watch loop and stdin thread.
    let out_mu: Arc<Mutex<()>> = Arc::new(Mutex::new(()));

    // Spawn stdin reader thread.
    {
        let out_clone = out_mu.clone();
        let threshold = block_threshold;
        let ref_h = ref_height;
        let shutdown_read = shutdown.clone();
        std::thread::spawn(move || {
            read_input(&out_clone, threshold, ref_h, &shutdown_read);
        });
    }

    let proto = Arc::new(proto);

    // Replay recent history before tailing. The tailer is tail-only (records
    // each file at its current end at startup), so it never re-renders these.
    if let Some(minutes) = args.catch_up {
        catch_up(&dir, minutes, block_threshold, ref_height, &out_mu, &proto);
    }

    // Main watch loop.
    let rx = watch::watch(dir, POLL_INTERVAL, shutdown.clone());

    for line in &rx {
        if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
            break;
        }
        let segs = convo::extract(&line.data);
        if segs.is_empty() {
            continue;
        }
        // All segments of one entry share a role; mark and separate per entry.
        let style = role_style(&segs[0].role);
        let texts: Vec<&str> = segs.iter().map(|s| s.text.as_str()).collect();
        let _lock = out_mu.lock().unwrap();
        emit_entry(style, &texts, block_threshold, ref_height, &proto);
    }

    0
}

/// Enable bracketed paste (only `main` writes stdout).
const PASTE_ON: &[u8] = b"\x1b[?2004h";
/// Disable bracketed paste.
const PASTE_OFF: &[u8] = b"\x1b[?2004l";
/// Terminal bell signalling rejected (typed) input.
const BEL: &[u8] = b"\x07";
/// Minimum gap between BEL beeps so a held key / typed sentence doesn't
/// machine-gun the bell.
const BEEP_THROTTLE: Duration = Duration::from_millis(250);
/// Timed-read interval for the raw-input loop; bounds shutdown latency.
const READ_TIMEOUT: Duration = Duration::from_millis(100);

/// Per-role styling for an emitted entry, so the feed is easy to scan: user,
/// assistant/agent, and manually-pasted text. Each entry gets a bold ANSI
/// color-coded opening glyph marker, the prose body tinted in the same color
/// (non-bold) so the whole entry reads as one role at a glance / mid-scroll,
/// and a bold closing glyph mirroring the open. The body color is reset by the
/// closing marker (which ends with a reset) so the blank separator after the
/// entry — and the manual `═` rule — stay untinted.
struct EntryStyle {
    /// Bold color + opening glyph + reset + trailing "> " forward-arrow.
    open: &'static str,
    /// Bold color + leading "<" back-arrow + glyph + reset.
    close: &'static str,
    /// Non-bold color set after `open`, carried across the terminal's soft-wraps.
    body: &'static str,
}
const USER_STYLE: EntryStyle = EntryStyle {
    open: "\x1b[1;32m(u)>\x1b[0m ",
    close: "\x1b[1;32m<(u)\x1b[0m",
    body: "\x1b[32m",
}; // green
const ASSISTANT_STYLE: EntryStyle = EntryStyle {
    open: "\x1b[1;36m[a]>\x1b[0m ",
    close: "\x1b[1;36m<[a]\x1b[0m",
    body: "\x1b[36m",
}; // cyan
const PASTE_STYLE: EntryStyle = EntryStyle {
    open: "\x1b[1;35m{p}>\x1b[0m ",
    close: "\x1b[1;35m<{p}\x1b[0m",
    body: "\x1b[35m",
}; // magenta

/// ANSI reset (SGR 0) closing the color spans above and the separator below.
const SGR_RESET: &str = "\x1b[0m";
/// Manual-separator rule: bold yellow, distinct from the role-marker colors.
const SEPARATOR_SGR: &str = "\x1b[1;33m";
/// Separator glyph: U+2550 box-drawings double horizontal (solid double line).
const SEPARATOR_CHAR: &str = "═";

/// Tracks whether the last thing written to stdout was a manual separator
/// (Enter in the viewing window). Real content (`emit_entry`) clears it; the
/// read loop refuses to stack a second separator and beeps instead.
static LAST_WAS_SEPARATOR: AtomicBool = AtomicBool::new(false);

/// Read manual input PASTE-ONLY: pasted text is captured silently (terminal
/// echo is off) and rendered through the same path as a conversation entry;
/// ordinary typing is ignored with a throttled BEL. Escape sequences (arrow
/// keys, etc.) and control bytes are consumed silently.
fn read_input(out_mu: &Mutex<()>, block_threshold: u32, ref_height: u32, shutdown: &AtomicBool) {
    let proto = match graphics::select() {
        Some(p) => p,
        None => {
            logging::warn("read_input: no graphics protocol; manual paste input disabled");
            return;
        }
    };
    let mut raw = match termbg::raw_input() {
        Some(r) => r,
        None => {
            logging::warn("read_input: could not enter raw stdin mode; manual paste input disabled");
            return;
        }
    };
    logging::info("read_input: raw stdin mode active; paste-only input enabled");

    enable_bracketed_paste();

    let mut parser = PasteParser::new();
    let mut last_beep: Option<Instant> = None;
    let mut buf = [0u8; 4096];

    loop {
        if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
            break;
        }
        let n = match raw.read(&mut buf, READ_TIMEOUT) {
            Some(n) => n,
            None => break,
        };
        for &byte in &buf[..n] {
            match parser.feed(byte) {
                Some(PasteEvent::PasteComplete(s)) => {
                    let _lock = out_mu.lock().unwrap();
                    emit_entry(&PASTE_STYLE, &[&s], block_threshold, ref_height, &proto);
                }
                Some(PasteEvent::Newline) => {
                    let _lock = out_mu.lock().unwrap();
                    // Enter inserts a deliberate separator (rule line). Refuse
                    // to stack a second one in a row — beep instead.
                    if LAST_WAS_SEPARATOR.swap(true, std::sync::atomic::Ordering::Relaxed) {
                        throttled_beep(&mut last_beep);
                    } else {
                        write_separator();
                    }
                }
                Some(PasteEvent::RejectTyping) => throttled_beep(&mut last_beep),
                None => {}
            }
        }
    }

    disable_bracketed_paste();
}

/// Emit one BEL, at most once per `BEEP_THROTTLE`, to avoid machine-gunning the
/// bell on a held key.
fn throttled_beep(last_beep: &mut Option<Instant>) {
    if last_beep.is_none_or(|t| t.elapsed() >= BEEP_THROTTLE) {
        beep();
        *last_beep = Some(Instant::now());
    }
}

/// Write a deliberate visual separator: a blank line, a terminal-width rule of
/// the double-line glyph (color-coded), and a trailing newline. Caller holds
/// the output mutex.
fn write_separator() {
    let rule = SEPARATOR_CHAR.repeat(termbg::term_width());
    let mut out = std::io::stdout();
    let _ = write!(out, "\n{SEPARATOR_SGR}{rule}{SGR_RESET}\n");
    let _ = out.flush();
}

fn enable_bracketed_paste() {
    let mut out = std::io::stdout();
    let _ = out.write_all(PASTE_ON);
    let _ = out.flush();
}

fn disable_bracketed_paste() {
    let mut out = std::io::stdout();
    let _ = out.write_all(PASTE_OFF);
    let _ = out.flush();
}

fn beep() {
    let mut out = std::io::stdout();
    let _ = out.write_all(BEL);
    let _ = out.flush();
}

/// Event produced by [`PasteParser`] from the raw input stream.
#[derive(Debug, PartialEq)]
enum PasteEvent {
    /// A complete bracketed paste; carries the inner (lossy-UTF8) text.
    PasteComplete(String),
    /// The user pressed Enter/Return outside a paste — a request for a manual
    /// separator.
    Newline,
    /// The user typed (rather than pasted) printable input — to be rejected.
    RejectTyping,
}

/// Internal parser state.
#[derive(PartialEq)]
enum PasteState {
    /// Outside a paste, matching nothing special.
    Idle,
    /// Matched some prefix of the start marker `ESC [ 2 0 0 ~`; `matched` is
    /// how many bytes of [`PASTE_START`] have matched so far (≥1).
    StartMarker { matched: usize },
    /// Inside a paste, buffering bytes and matching the end marker; `end_matched`
    /// is how many bytes of [`PASTE_END`] have matched at the buffer tail.
    Capturing { end_matched: usize },
    /// Consuming an escape sequence (arrow keys, etc.) outside a paste.
    Escape,
}

const PASTE_START: &[u8] = b"\x1b[200~";
const PASTE_END: &[u8] = b"\x1b[201~";

/// A byte-at-a-time state machine recognising bracketed-paste markers and
/// classifying non-paste input. Pure and unit-testable: no I/O.
struct PasteParser {
    state: PasteState,
    buf: Vec<u8>,
}

impl PasteParser {
    fn new() -> Self {
        PasteParser {
            state: PasteState::Idle,
            buf: Vec::new(),
        }
    }

    /// Feed one byte; return an event when one is recognised.
    fn feed(&mut self, byte: u8) -> Option<PasteEvent> {
        match self.state {
            PasteState::Idle => self.feed_idle(byte),
            PasteState::StartMarker { matched } => self.feed_start_marker(matched, byte),
            PasteState::Capturing { end_matched } => self.feed_capturing(end_matched, byte),
            PasteState::Escape => {
                // Consume a CSI/escape sequence: a final byte (>=0x40) ends it.
                if byte >= 0x40 {
                    self.state = PasteState::Idle;
                }
                None
            }
        }
    }

    fn feed_idle(&mut self, byte: u8) -> Option<PasteEvent> {
        if byte == 0x1b {
            self.state = PasteState::StartMarker { matched: 1 };
            None
        } else if byte == b'\n' || byte == b'\r' {
            // Enter/Return: a request for a manual separator.
            Some(PasteEvent::Newline)
        } else if byte < 0x20 {
            // Other control bytes: consume silently.
            None
        } else {
            // Printable ASCII or UTF-8 lead/continuation byte: typed input.
            Some(PasteEvent::RejectTyping)
        }
    }

    fn feed_start_marker(&mut self, matched: usize, byte: u8) -> Option<PasteEvent> {
        if byte == PASTE_START[matched] {
            let next = matched + 1;
            if next == PASTE_START.len() {
                self.buf.clear();
                self.state = PasteState::Capturing { end_matched: 0 };
            } else {
                self.state = PasteState::StartMarker { matched: next };
            }
            None
        } else {
            // Not the paste start — it was some other escape sequence
            // (e.g. ESC [ A for up arrow). Consume it silently.
            self.state = if byte >= 0x40 {
                PasteState::Idle
            } else {
                PasteState::Escape
            };
            None
        }
    }

    fn feed_capturing(&mut self, end_matched: usize, byte: u8) -> Option<PasteEvent> {
        if byte == PASTE_END[end_matched] {
            let next = end_matched + 1;
            if next == PASTE_END.len() {
                let text = String::from_utf8_lossy(&self.buf).into_owned();
                self.buf.clear();
                self.state = PasteState::Idle;
                return Some(PasteEvent::PasteComplete(text));
            }
            self.state = PasteState::Capturing { end_matched: next };
            None
        } else {
            // Partial end-marker match broke: those bytes were real content.
            if end_matched > 0 {
                self.buf.extend_from_slice(&PASTE_END[..end_matched]);
                // Re-test this byte against a fresh end-marker scan.
                self.state = PasteState::Capturing { end_matched: 0 };
                return self.feed_capturing(0, byte);
            }
            self.buf.push(byte);
            self.state = PasteState::Capturing { end_matched: 0 };
            None
        }
    }
}

/// Emit one conversation/paste entry: the bold opening role marker, the body
/// tinted in the role's non-bold color, each of the entry's texts rendered (math
/// inline if short, on its own line if tall; prose mirrored verbatim), then the
/// bold closing marker (inline at the end of the content, or on its own line
/// when the content left the cursor at column 0 — e.g. an image-final entry),
/// then a single blank-line separator. Returns the number of math segments
/// emitted (render attempts). Emits nothing (no marker) when the entry has no
/// renderable content.
fn emit_entry(
    style: &EntryStyle,
    texts: &[&str],
    block_threshold: u32,
    ref_height: u32,
    proto: &graphics::Protocol,
) -> usize {
    let scanned: Vec<Vec<mathscan::Segment>> = texts.iter().map(|t| mathscan::scan(t)).collect();
    if scanned.iter().all(Vec::is_empty) {
        return 0;
    }

    // Real content breaks the manual-separator run, so the next Enter draws one.
    LAST_WAS_SEPARATOR.store(false, std::sync::atomic::Ordering::Relaxed);

    let stdout = std::io::stdout();
    let mut out = stdout.lock();

    let _ = write!(out, "{}{}", style.open, style.body);
    let mut at_line_start = false; // marker just written
    let mut math_count = 0usize;
    for segs in &scanned {
        math_count += emit_segments(&mut out, segs, &mut at_line_start, block_threshold, ref_height, proto);
    }

    // Close marker mirrors the open: inline after mid-line content (separated by
    // a space), or on its own fresh line when the content ended at column 0
    // (image-final). The close marker ends with a reset, so the body color does
    // not leak into the blank separator below.
    if at_line_start {
        let _ = writeln!(out, "{}", style.close);
    } else {
        let _ = writeln!(out, " {}", style.close);
    }
    let _ = writeln!(out);
    math_count
}

/// Walk a flat segment stream, writing text verbatim and rendering math in
/// place. `at_line_start` carries cursor state across calls (so a leading marker
/// and successive text blocks share it). Returns the number of math segments
/// emitted. The caller is responsible for any trailing newline.
fn emit_segments(
    out: &mut impl Write,
    segs: &[mathscan::Segment],
    at_line_start: &mut bool,
    block_threshold: u32,
    ref_height: u32,
    proto: &graphics::Protocol,
) -> usize {
    let mut math_count = 0usize;

    for seg in segs {
        match seg.kind {
            mathscan::Kind::Text => {
                if !seg.text.is_empty() {
                    let _ = write!(out, "{}", seg.text);
                    *at_line_start = seg.text.ends_with('\n');
                }
            }
            mathscan::Kind::Math => {
                math_count += 1;
                match render::render(&seg.text, seg.display) {
                    Ok((png, height)) if height >= block_threshold => {
                        let rows = rows_for(height, ref_height);
                        if !*at_line_start {
                            let _ = writeln!(out);
                        }
                        let _ = out.write_all(&proto.encode(&png, rows));
                        let _ = writeln!(out);
                        *at_line_start = true;
                    }
                    Ok((png, height)) => {
                        let rows = rows_for(height, ref_height);
                        let _ = out.write_all(&proto.encode(&png, rows));
                        *at_line_start = false;
                    }
                    Err(e) => {
                        // Pass raw LaTeX through on failure.
                        logging::warn(&format!("render failed ({e}), passing through raw latex"));
                        let delim = if seg.display { "$$" } else { "$" };
                        let _ = write!(out, "{delim}{}{delim}", seg.text);
                        *at_line_start = false;
                    }
                }
            }
        }
    }

    math_count
}

/// Per-role entry style for a conversation entry, by its `role`.
fn role_style(role: &str) -> &'static EntryStyle {
    match role {
        "user" => &USER_STYLE,
        _ => &ASSISTANT_STYLE,
    }
}

/// Configure the render theme from the detected terminal background.
///
/// When `opaque_bg` is false (kitty/imgcat) glyphs render on a transparent
/// background. When true (sixel) the background is opaque: the detected
/// terminal color (so the sixel box blends in) or white when undetected.
fn apply_theme(opaque_bg: bool) {
    match termbg::query(BG_QUERY_TIMEOUT) {
        None => {
            logging::info("terminal background not detected; using white background fallback");
            render::set_theme(Color::BLACK, Color::WHITE);
            if opaque_bg {
                sixel::set_background(255, 255, 255);
            }
        }
        Some((r, g, b)) => {
            let (glyph, label) = if termbg::is_dark(r, g, b) {
                (Color::WHITE, "dark terminal background; light glyphs")
            } else {
                (Color::BLACK, "light terminal background; dark glyphs")
            };
            let background = if opaque_bg {
                logging::info(&format!("{label} on opaque detected background"));
                sixel::set_background(r, g, b);
                Color::new(
                    f32::from(r) / 255.0,
                    f32::from(g) / 255.0,
                    f32::from(b) / 255.0,
                    1.0,
                )
            } else {
                logging::info(&format!("{label} on transparent"));
                Color::new(0.0, 0.0, 0.0, 0.0)
            };
            render::set_theme(glyph, background);
        }
    }
}

const DEFAULT_REF_HEIGHT: u32 = 42;

/// Rendered pixel height of a reference capital "X". A math image is scaled to
/// a row count proportional to its height relative to this, so a capital X
/// displays as exactly one text row and everything scales with surrounding text.
fn reference_height() -> u32 {
    match render::render("X", false) {
        Ok((_, h)) if h > 0 => h,
        _ => DEFAULT_REF_HEIGHT,
    }
}

/// Visual scale bump so math reads at the surrounding text's full line height
/// rather than just cap-height: a capital X alone maps to ~1 row, but everything
/// is nudged up so blocks don't read undersized against the prose around them.
const ROW_SCALE: f32 = 1.25;

/// Row (cell) count for an image of `height_px`, relative to the reference X
/// height `ref_px`, scaled by `ROW_SCALE`. Rounds to nearest and clamps to ≥1.
fn rows_for(height_px: u32, ref_px: u32) -> u32 {
    ((height_px as f32 / ref_px as f32 * ROW_SCALE).round() as u32).max(1)
}


/// Render math from conversation entries timestamped within the last `minutes`,
/// across every *.jsonl file in `dir`, in timestamp order. Runs on the main
/// thread before the tail watch starts.
fn catch_up(
    dir: &Path,
    minutes: i64,
    block_threshold: u32,
    ref_height: u32,
    out_mu: &Mutex<()>,
    proto: &graphics::Protocol,
) {
    let cutoff = Utc::now() - chrono::Duration::minutes(minutes);

    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(e) => {
            logging::warn(&format!("catch-up: cannot read {}: {e}", dir.display()));
            return;
        }
    };

    let mut recent: Vec<(DateTime<Utc>, convo::Segment)> = Vec::new();
    for entry in entries.flatten() {
        let path = entry.path();
        if path.extension().and_then(|e| e.to_str()) != Some("jsonl") {
            continue;
        }
        let data = match std::fs::read(&path) {
            Ok(d) => d,
            Err(e) => {
                logging::warn(&format!("catch-up: cannot read {}: {e}", path.display()));
                continue;
            }
        };
        for line in data.split(|&b| b == b'\n') {
            if line.is_empty() {
                continue;
            }
            if let Some(parsed) = convo::parse(line) {
                if let Some(ts) = within_window(parsed.timestamp.as_deref(), cutoff) {
                    for seg in parsed.segments {
                        recent.push((ts, seg));
                    }
                }
            }
        }
    }

    recent.sort_by_key(|(ts, _)| *ts);

    let segments = recent.len();
    let mut rendered = 0usize;
    for (_, seg) in &recent {
        let _lock = out_mu.lock().unwrap();
        rendered += emit_entry(role_style(&seg.role), &[&seg.text], block_threshold, ref_height, proto);
    }
    logging::info(&format!(
        "catch-up: rendered {rendered} expression(s) from {segments} text segment(s) in the last {minutes}m"
    ));
}

/// Parse an RFC3339 timestamp and return it (as UTC) if it is at or after
/// `cutoff`; otherwise None. An absent or unparseable timestamp is excluded.
fn within_window(timestamp: Option<&str>, cutoff: DateTime<Utc>) -> Option<DateTime<Utc>> {
    let ts = DateTime::parse_from_rfc3339(timestamp?)
        .ok()?
        .with_timezone(&Utc);
    (ts >= cutoff).then_some(ts)
}

/// Derive the Claude Code conversation-log directory for the current working
/// directory: ~/.claude/projects/<cwd with '/', '\', and ':' replaced by '-'>.
fn project_log_dir() -> Result<PathBuf, String> {
    let cwd = std::env::current_dir().map_err(|e| format!("getwd: {e}"))?;
    let home = dirs_home().ok_or_else(|| "home dir: not found".to_string())?;
    let name = cwd.to_string_lossy().replace(['/', '\\', ':'], "-");
    Ok(home.join(".claude").join("projects").join(name))
}

fn dirs_home() -> Option<PathBuf> {
    #[cfg(unix)]
    {
        std::env::var_os("HOME").map(PathBuf::from)
    }
    #[cfg(windows)]
    {
        // Claude Code on Windows stores data under %USERPROFILE%\.claude.
        std::env::var_os("USERPROFILE")
            .or_else(|| std::env::var_os("HOME"))
            .map(PathBuf::from)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn argv(a: &[&str]) -> Vec<String> {
        a.iter().map(|s| s.to_string()).collect()
    }

    /// Unwrap a successful parse to its `Args`, panicking on any other outcome.
    fn run_args(argv: Vec<String>) -> Args {
        match parse_args(argv) {
            Ok(Invocation::Run(a)) => a,
            _ => panic!("expected Invocation::Run"),
        }
    }

    #[test]
    fn no_args_defaults() {
        let a = run_args(argv(&[]));
        assert!(a.log.is_none());
        assert!(a.catch_up.is_none());
    }

    #[test]
    fn help_returns_help_variant() {
        assert!(matches!(parse_args(argv(&["--help"])), Ok(Invocation::Help)));
        assert!(matches!(parse_args(argv(&["-h"])), Ok(Invocation::Help)));
    }

    #[test]
    fn version_returns_version_variant() {
        assert!(matches!(parse_args(argv(&["--version"])), Ok(Invocation::Version)));
        assert!(matches!(parse_args(argv(&["-V"])), Ok(Invocation::Version)));
    }

    #[test]
    fn log_space_and_equals_forms() {
        let a = run_args(argv(&["--log", "/tmp/x.log"]));
        assert_eq!(a.log, Some(PathBuf::from("/tmp/x.log")));
        let b = run_args(argv(&["--log=/tmp/y.log"]));
        assert_eq!(b.log, Some(PathBuf::from("/tmp/y.log")));
    }

    #[test]
    fn catch_up_bare_and_explicit() {
        let bare = run_args(argv(&["--catch-up"]));
        assert_eq!(bare.catch_up, Some(DEFAULT_CATCH_UP_MINUTES));
        let explicit = run_args(argv(&["--catch-up=20"]));
        assert_eq!(explicit.catch_up, Some(20));
    }

    #[test]
    fn errors() {
        assert!(parse_args(argv(&["--nope"])).is_err());
        assert!(parse_args(argv(&["--log"])).is_err());
        assert!(parse_args(argv(&["--log="])).is_err());
        assert!(parse_args(argv(&["--catch-up=abc"])).is_err());
        assert!(parse_args(argv(&["--catch-up=-3"])).is_err());
    }

    #[test]
    fn rows_for_rounds_and_clamps() {
        // Scaled by ROW_SCALE (1.25), then rounded to nearest.
        assert_eq!(rows_for(42, 42), 1); // 1.0 * 1.25 = 1.25 -> 1
        assert_eq!(rows_for(126, 42), 4); // 3.0 * 1.25 = 3.75 -> 4
        assert_eq!(rows_for(59, 42), 2); // 1.40 * 1.25 = 1.76 -> 2
        assert_eq!(rows_for(67, 42), 2); // 1.60 * 1.25 = 1.99 -> 2
        // Clamps to at least 1 even for tiny images.
        assert_eq!(rows_for(0, 42), 1);
        assert_eq!(rows_for(5, 42), 1); // 0.12 * 1.25 = 0.15 -> 0 -> clamp 1
    }

    fn feed_all(p: &mut PasteParser, bytes: &[u8]) -> Vec<PasteEvent> {
        bytes.iter().filter_map(|&b| p.feed(b)).collect()
    }

    #[test]
    fn clean_paste_yields_one_complete() {
        let mut p = PasteParser::new();
        let input = b"\x1b[200~hello $x$ world\x1b[201~";
        let events = feed_all(&mut p, input);
        assert_eq!(
            events,
            vec![PasteEvent::PasteComplete("hello $x$ world".to_string())]
        );
    }

    #[test]
    fn multiline_paste_preserves_newlines() {
        let mut p = PasteParser::new();
        let input = b"\x1b[200~line1\nline2\n$$y$$\x1b[201~";
        let events = feed_all(&mut p, input);
        assert_eq!(
            events,
            vec![PasteEvent::PasteComplete("line1\nline2\n$$y$$".to_string())]
        );
    }

    #[test]
    fn markers_split_across_feeds_still_parse() {
        let mut p = PasteParser::new();
        // Start marker split byte-by-byte, content, end marker split.
        let mut events = Vec::new();
        for chunk in [
            &b"\x1b[2"[..],
            &b"00~"[..],
            &b"ab"[..],
            &b"\x1b[20"[..],
            &b"1~"[..],
        ] {
            events.extend(feed_all(&mut p, chunk));
        }
        assert_eq!(events, vec![PasteEvent::PasteComplete("ab".to_string())]);
    }

    #[test]
    fn typed_printable_rejected() {
        let mut p = PasteParser::new();
        assert_eq!(p.feed(b'a'), Some(PasteEvent::RejectTyping));
        assert_eq!(p.feed(b' '), Some(PasteEvent::RejectTyping));
        // UTF-8 lead byte counts as typed input too.
        assert_eq!(p.feed(0xc3), Some(PasteEvent::RejectTyping));
    }

    #[test]
    fn arrow_key_escape_is_silent() {
        let mut p = PasteParser::new();
        // ESC [ A — up arrow — outside paste, no RejectTyping.
        assert_eq!(feed_all(&mut p, b"\x1b[A"), vec![]);
        // Parser is back to idle and rejects subsequent typing.
        assert_eq!(p.feed(b'z'), Some(PasteEvent::RejectTyping));
    }

    #[test]
    fn control_bytes_silent() {
        let mut p = PasteParser::new();
        // Tab and Ctrl-C produce no event (CR/LF are Newline, tested separately).
        assert_eq!(feed_all(&mut p, b"\t\x03"), vec![]);
    }

    #[test]
    fn role_marker_maps_user_and_assistant() {
        assert_eq!(role_style("user").open, USER_STYLE.open);
        assert_eq!(role_style("assistant").open, ASSISTANT_STYLE.open);
        // Unknown roles fall back to the assistant style.
        assert_eq!(role_style("system").open, ASSISTANT_STYLE.open);
    }

    #[test]
    fn enter_yields_newline_outside_paste() {
        let mut p = PasteParser::new();
        assert_eq!(p.feed(b'\n'), Some(PasteEvent::Newline));
        assert_eq!(p.feed(b'\r'), Some(PasteEvent::Newline));
    }

    #[test]
    fn newlines_inside_paste_are_content_not_separators() {
        let mut p = PasteParser::new();
        assert_eq!(
            feed_all(&mut p, b"\x1b[200~a\nb\rc\x1b[201~"),
            vec![PasteEvent::PasteComplete("a\nb\rc".to_string())]
        );
    }

    #[test]
    fn end_marker_false_start_kept_as_content() {
        let mut p = PasteParser::new();
        // Content contains a lone ESC that does not begin the end marker.
        let input = b"\x1b[200~a\x1bb\x1b[201~";
        let events = feed_all(&mut p, input);
        assert_eq!(
            events,
            vec![PasteEvent::PasteComplete("a\x1bb".to_string())]
        );
    }

    #[test]
    fn window_filters_by_cutoff() {
        let cutoff = DateTime::parse_from_rfc3339("2026-05-27T12:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        assert!(within_window(Some("2026-05-27T12:30:00Z"), cutoff).is_some());
        assert!(within_window(Some("2026-05-27T12:00:00Z"), cutoff).is_some());
        assert!(within_window(Some("2026-05-27T11:59:59Z"), cutoff).is_none());
        assert!(within_window(Some("not-a-date"), cutoff).is_none());
        assert!(within_window(None, cutoff).is_none());
    }
}
