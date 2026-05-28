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

use std::io::{BufRead, Write};
use std::path::{Path, PathBuf};
use std::sync::{atomic::AtomicBool, Arc, Mutex};
use std::time::Duration;

use chrono::{DateTime, Utc};
use ratex_types::color::Color;

const POLL_INTERVAL: Duration = Duration::from_millis(500);
const BG_QUERY_TIMEOUT: Duration = Duration::from_millis(200);
const LOG_FILE_NAME: &str = "laterm.log";
const DEFAULT_CATCH_UP_MINUTES: i64 = 5;

const USAGE: &str = "\
usage: laterm [options]

Renders LaTeX math from the current Claude Code conversation log as inline
terminal images.

options:
  --log-file <PATH>     write diagnostics to PATH (default: laterm.log beside
                        the executable)
  --catch-up[=<MINS>]   render math from the last MINS minutes of history
                        before tailing (bare flag = 5 minutes)
  --help                show this help and exit
";

struct Args {
    log_file: Option<PathBuf>,
    catch_up: Option<i64>,
}

/// Parse argv (excluding the program name). On `--help` returns Ok(None) after
/// the caller is expected to print usage. On error returns Err with a message.
fn parse_args(argv: impl IntoIterator<Item = String>) -> Result<Option<Args>, String> {
    let mut args = Args {
        log_file: None,
        catch_up: None,
    };
    let mut it = argv.into_iter();

    while let Some(arg) = it.next() {
        if arg == "--help" || arg == "-h" {
            return Ok(None);
        } else if arg == "--log-file" {
            let path = it.next().ok_or("--log-file requires a path argument")?;
            args.log_file = Some(PathBuf::from(path));
        } else if let Some(path) = arg.strip_prefix("--log-file=") {
            args.log_file = Some(PathBuf::from(path));
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

    Ok(Some(args))
}

fn main() {
    std::process::exit(run());
}

fn run() -> i32 {
    let args = match parse_args(std::env::args().skip(1)) {
        Ok(Some(a)) => a,
        Ok(None) => {
            print!("{USAGE}");
            return 0;
        }
        Err(e) => {
            eprint!("laterm: {e}\n\n{USAGE}");
            return 2;
        }
    };

    logging::init(&resolve_log_path(args.log_file), logging::level_from_env());

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
        for seg in convo::extract(&line.data) {
            let _lock = out_mu.lock().unwrap();
            emit_text(&seg.text, block_threshold, ref_height, false, &proto);
        }
    }

    0
}

fn read_input(out_mu: &Mutex<()>, block_threshold: u32, ref_height: u32, shutdown: &AtomicBool) {
    let proto = match graphics::select() {
        Some(p) => p,
        None => return,
    };
    let stdin = std::io::stdin();
    for line in stdin.lock().lines() {
        if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
            break;
        }
        match line {
            Ok(text) => {
                let _lock = out_mu.lock().unwrap();
                emit_text(&text, block_threshold, ref_height, true, &proto);
            }
            Err(_) => break,
        }
    }
}

/// Echo text to stdout, rendering math inline (short) or on its own line
/// (tall). Non-math text is mirrored verbatim with newlines preserved.
/// When `allow_bare` is true and there's no delimited math, the whole trimmed
/// line is rendered as one bare inline LaTeX expression (manual-input mode).
/// Returns the number of math segments emitted (render attempts).
fn emit_text(
    text: &str,
    block_threshold: u32,
    ref_height: u32,
    allow_bare: bool,
    proto: &graphics::Protocol,
) -> usize {
    let segs = mathscan::scan(text);
    if segs.is_empty() {
        return 0;
    }
    let has_math = segs.iter().any(|s| s.kind == mathscan::Kind::Math);

    let stdout = std::io::stdout();
    let mut out = stdout.lock();

    if !has_math && allow_bare {
        let expr = text.trim().to_string();
        if expr.is_empty() {
            return 0;
        }
        let bare = vec![mathscan::Segment {
            kind: mathscan::Kind::Math,
            text: expr,
            display: false,
        }];
        return emit_segments(&mut out, &bare, block_threshold, ref_height, proto);
    }

    emit_segments(&mut out, &segs, block_threshold, ref_height, proto)
}

/// Walk a flat segment stream, writing text verbatim and rendering math in
/// place. Returns the number of math segments emitted.
fn emit_segments(
    out: &mut impl Write,
    segs: &[mathscan::Segment],
    block_threshold: u32,
    ref_height: u32,
    proto: &graphics::Protocol,
) -> usize {
    let mut at_line_start = true;
    let mut math_count = 0usize;

    for seg in segs {
        match seg.kind {
            mathscan::Kind::Text => {
                if !seg.text.is_empty() {
                    let _ = write!(out, "{}", seg.text);
                    at_line_start = seg.text.ends_with('\n');
                }
            }
            mathscan::Kind::Math => {
                math_count += 1;
                match render::render(&seg.text, seg.display) {
                    Ok((png, height)) if height >= block_threshold => {
                        let rows = rows_for(height, ref_height);
                        if !at_line_start {
                            let _ = writeln!(out);
                        }
                        let _ = out.write_all(&proto.encode(&png, rows));
                        let _ = writeln!(out);
                        at_line_start = true;
                    }
                    Ok((png, height)) => {
                        let rows = rows_for(height, ref_height);
                        let _ = out.write_all(&proto.encode(&png, rows));
                        at_line_start = false;
                    }
                    Err(e) => {
                        // Pass raw LaTeX through on failure.
                        logging::warn(&format!("render failed ({e}), passing through raw latex"));
                        let delim = if seg.display { "$$" } else { "$" };
                        let _ = write!(out, "{delim}{}{delim}", seg.text);
                        at_line_start = false;
                    }
                }
            }
        }
    }

    if !at_line_start {
        let _ = writeln!(out);
    }
    math_count
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

/// Resolve the log file path: an explicit `--log-file` wins; otherwise
/// `laterm.log` beside the executable, falling back to the cwd if the
/// executable directory is unavailable.
fn resolve_log_path(override_path: Option<PathBuf>) -> PathBuf {
    if let Some(p) = override_path {
        return p;
    }
    if let Some(dir) = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(Path::to_path_buf))
    {
        return dir.join(LOG_FILE_NAME);
    }
    PathBuf::from(LOG_FILE_NAME)
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
        rendered += emit_text(&seg.text, block_threshold, ref_height, false, proto);
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

    #[test]
    fn no_args_defaults() {
        let a = parse_args(argv(&[])).unwrap().unwrap();
        assert!(a.log_file.is_none());
        assert!(a.catch_up.is_none());
    }

    #[test]
    fn help_returns_none() {
        assert!(parse_args(argv(&["--help"])).unwrap().is_none());
    }

    #[test]
    fn log_file_space_and_equals_forms() {
        let a = parse_args(argv(&["--log-file", "/tmp/x.log"]))
            .unwrap()
            .unwrap();
        assert_eq!(a.log_file, Some(PathBuf::from("/tmp/x.log")));
        let b = parse_args(argv(&["--log-file=/tmp/y.log"]))
            .unwrap()
            .unwrap();
        assert_eq!(b.log_file, Some(PathBuf::from("/tmp/y.log")));
    }

    #[test]
    fn catch_up_bare_and_explicit() {
        let bare = parse_args(argv(&["--catch-up"])).unwrap().unwrap();
        assert_eq!(bare.catch_up, Some(DEFAULT_CATCH_UP_MINUTES));
        let explicit = parse_args(argv(&["--catch-up=20"])).unwrap().unwrap();
        assert_eq!(explicit.catch_up, Some(20));
    }

    #[test]
    fn errors() {
        assert!(parse_args(argv(&["--nope"])).is_err());
        assert!(parse_args(argv(&["--log-file"])).is_err());
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
