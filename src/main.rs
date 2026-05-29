// laterm — Claude Code sidecar that renders LaTeX math from the conversation
// log as inline terminal images. (Rust implementation; the validated Go
// prototype lives in prototype/.)

mod convo;
mod feed;
mod graphics;
mod input;
mod logging;
mod mathscan;
mod render;
mod sixel;
mod termbg;
mod watch;

use std::path::{Path, PathBuf};
use std::sync::{atomic::AtomicBool, Arc, Mutex};
use std::time::Duration;

use chrono::{DateTime, Utc};
use ratex_types::color::Color;

use feed::RenderCtx;

const POLL_INTERVAL: Duration = Duration::from_millis(250);
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

    // Select the graphics protocol once — hard gate. The single selected
    // protocol is shared (Arc) by the watch loop, catch-up, and the reader
    // thread, so the DA1/sixel probe runs exactly once at startup.
    let proto = match graphics::select() {
        Some(p) => Arc::new(p),
        None => {
            eprintln!("laterm: terminal supports none of the kitty graphics, iTerm2 (imgcat), or sixel protocols; run inside a compatible terminal (kitty, ghostty, iTerm2, WezTerm, or a sixel-capable terminal)");
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

    // Build the render context once: renders the reference "X" to derive the
    // block threshold (1.5×) and proportional row sizing, and carries the proto.
    let ctx = Arc::new(RenderCtx::new(proto));

    // Stdout lock shared between watch loop and stdin thread.
    let out_mu: Arc<Mutex<()>> = Arc::new(Mutex::new(()));

    // Spawn stdin reader thread (paste-only manual input).
    {
        let out_clone = out_mu.clone();
        let ctx_read = ctx.clone();
        let shutdown_read = shutdown.clone();
        std::thread::spawn(move || {
            input::read_input(&out_clone, &ctx_read, &shutdown_read);
        });
    }

    // Replay recent history before tailing. The tailer is tail-only (records
    // each file at its current end at startup), so it never re-renders these.
    if let Some(minutes) = args.catch_up {
        catch_up(&dir, minutes, &out_mu, &ctx);
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
        let style = feed::role_style(&segs[0].role);
        let texts: Vec<&str> = segs.iter().map(|s| s.text.as_str()).collect();
        let _lock = out_mu.lock().unwrap();
        feed::emit_entry(style, &texts, &ctx);
    }

    0
}

/// Render math from conversation entries timestamped within the last `minutes`,
/// across every *.jsonl file in `dir`, in timestamp order. Runs on the main
/// thread before the tail watch starts.
///
/// Each replayed entry is emitted with a single `emit_entry` call covering all
/// its text segments — one marker-pair per entry — so caught-up framing is
/// byte-identical to the live tail (which also emits once per jsonl entry).
fn catch_up(dir: &Path, minutes: i64, out_mu: &Mutex<()>, ctx: &RenderCtx) {
    let cutoff = Utc::now() - chrono::Duration::minutes(minutes);

    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(e) => {
            logging::warn(&format!("catch-up: cannot read {}: {e}", dir.display()));
            return;
        }
    };

    // One element per source jsonl entry, keeping its segments grouped so the
    // emit framing matches the live path (one marker-pair per entry).
    let mut recent: Vec<(DateTime<Utc>, Vec<convo::Segment>)> = Vec::new();
    for entry in entries.flatten() {
        let path = entry.path();
        if !watch::is_jsonl(&path) {
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
            if let Some(parsed) = convo::parse(line)
                && let Some(ts) = within_window(parsed.timestamp.as_deref(), cutoff)
            {
                recent.push((ts, parsed.segments));
            }
        }
    }

    recent.sort_by_key(|(ts, _)| *ts);

    let entry_count = recent.len();
    let mut rendered = 0usize;
    for (_, segs) in &recent {
        // All segments of one entry share a role; emit once per entry.
        let style = feed::role_style(&segs[0].role);
        let texts: Vec<&str> = segs.iter().map(|s| s.text.as_str()).collect();
        let _lock = out_mu.lock().unwrap();
        rendered += feed::emit_entry(style, &texts, ctx);
    }
    logging::info(&format!(
        "catch-up: rendered {rendered} expression(s) from {entry_count} entr(ies) in the last {minutes}m"
    ));
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
