// laterm — Claude Code sidecar that renders LaTeX math from the conversation as
// inline terminal images. Live turns arrive over a Unix-domain socket, pushed by
// Claude Code hooks (see `--install-hooks` / `--hook`); `--catch-up` replays the
// completed transcript. (Rust implementation; the validated Go prototype lives in
// prototype/.)

mod convo;
mod feed;
mod graphics;
mod hook;
mod input;
mod ipc;
mod logging;
mod mathscan;
mod render;
mod sixel;
mod termbg;

use std::io::Read;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, atomic::AtomicBool};
use std::time::Duration;

use chrono::{DateTime, Utc};
use ratex_types::color::Color;
use serde_json::{Value, json};

use feed::RenderCtx;

const BG_QUERY_TIMEOUT: Duration = Duration::from_millis(200);
const DEFAULT_CATCH_UP_MINUTES: i64 = 5;

const USAGE: &str = concat!(
    "laterm ",
    env!("CARGO_PKG_VERSION"),
    "\n",
    "\n",
    "usage: laterm [options]\n",
    "\n",
    "Renders LaTeX math from the current Claude Code conversation as inline\n",
    "terminal images. Live turns are pushed by Claude Code hooks over a\n",
    "Unix-domain socket; run --install-hooks once to configure them.\n",
    "\n",
    "options:\n",
    "  --cwd <PATH>, -C      derive the watched log dir and rendezvous socket from\n",
    "                        PATH instead of the current working directory\n",
    "  --log <PATH>          write diagnostics to PATH (default: no logging)\n",
    "  --catch-up[=<MINS>]   replay math from the last MINS minutes of completed\n",
    "                        transcript before the live path (bare flag = 5 minutes)\n",
    "  --install-hooks [--project|--global]\n",
    "                        merge laterm's two hook entries into settings.json\n",
    "                        (default --project) and exit\n",
    "  --hook <event>        forward a Claude Code hook payload to the running\n",
    "                        window and exit (invoked BY Claude Code, not by you)\n",
    "  --version, -V         print version and exit\n",
    "  --help                show this help and exit\n",
);

struct Args {
    cwd: Option<PathBuf>,
    log: Option<PathBuf>,
    catch_up: Option<i64>,
}

/// Which settings.json `--install-hooks` writes: the repo-local `.claude/` or the
/// user-global `~/.claude/`.
#[derive(Debug, PartialEq, Eq, Clone, Copy)]
enum HookTarget {
    Project,
    Global,
}

/// The outcome of parsing argv. Each variant is a distinct terminal action.
enum Invocation {
    Run(Args),
    /// `--hook <event>`: forward the stdin payload and exit.
    Hook(String),
    /// `--install-hooks`: merge hook entries into settings.json and exit.
    InstallHooks(HookTarget),
    Help,
    Version,
}

/// Parse argv (excluding the program name). Returns the [`Invocation`] to
/// perform, or Err with a message on a malformed argument.
fn parse_args(argv: impl IntoIterator<Item = String>) -> Result<Invocation, String> {
    let mut args = Args {
        cwd: None,
        log: None,
        catch_up: None,
    };
    let mut install = false;
    let mut target: Option<HookTarget> = None;
    let mut it = argv.into_iter();

    while let Some(arg) = it.next() {
        if arg == "--help" || arg == "-h" {
            return Ok(Invocation::Help);
        } else if arg == "--version" || arg == "-V" {
            return Ok(Invocation::Version);
        } else if arg == "--hook" {
            let event = it
                .next()
                .filter(|e| !e.is_empty())
                .ok_or("--hook requires an event argument (UserPromptSubmit or Stop)")?;
            return Ok(Invocation::Hook(event));
        } else if let Some(event) = arg.strip_prefix("--hook=") {
            if event.is_empty() {
                return Err(
                    "--hook requires an event argument (UserPromptSubmit or Stop)".to_string(),
                );
            }
            return Ok(Invocation::Hook(event.to_string()));
        } else if arg == "--install-hooks" {
            install = true;
        } else if arg == "--project" {
            target = Some(HookTarget::Project);
        } else if arg == "--global" {
            target = Some(HookTarget::Global);
        } else if arg == "--cwd" || arg == "-C" {
            let path = it.next().ok_or("--cwd requires a path argument")?;
            args.cwd = Some(PathBuf::from(path));
        } else if let Some(path) = arg.strip_prefix("--cwd=") {
            if path.is_empty() {
                return Err("--cwd requires a path argument".to_string());
            }
            args.cwd = Some(PathBuf::from(path));
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

    if install {
        return Ok(Invocation::InstallHooks(
            target.unwrap_or(HookTarget::Project),
        ));
    }
    if target.is_some() {
        return Err("--project/--global are only valid with --install-hooks".to_string());
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
        // Forward-and-exit: dispatched before ANY other startup work. Read the
        // hook JSON from stdin, derive the rendezvous socket from the payload's
        // cwd (the authoritative project dir), forward the raw bytes. On ANY
        // failure (bad stdin, no cwd, no listener) exit 0 SILENTLY — never block
        // or fail Claude Code — and write NOTHING to stdout.
        Ok(Invocation::Hook(_event)) => {
            let mut bytes = Vec::new();
            if std::io::stdin().read_to_end(&mut bytes).is_err() {
                return 0;
            }
            if let Some(cwd) = hook::payload_cwd(&bytes) {
                let _ = ipc::forward(Path::new(&cwd), &bytes);
            }
            return 0;
        }
        Ok(Invocation::InstallHooks(target)) => return install_hooks(target),
        Err(e) => {
            eprint!("laterm: {e}\n\n{USAGE}");
            return 2;
        }
    };

    // Honor `-C`/`--cwd` before anything reads the CWD. The existing
    // `current_dir()`-based derivation then yields the same canonical absolute
    // path the OS gives Claude Code, so the dir-name mangling (log dir + socket)
    // matches by construction (no manual canonicalization). Nothing before dir
    // derivation depends on CWD (logging is explicit; protocol/theme don't use it).
    if let Some(p) = &args.cwd
        && let Err(e) = std::env::set_current_dir(p)
    {
        eprintln!("laterm: cannot change directory to {}: {e}", p.display());
        return 1;
    }

    // No logging by default — opt in with `--log <PATH>`. Avoids multiple
    // instances clobbering one shared default file; enable it for diagnostics.
    if let Some(path) = &args.log {
        logging::init(path, logging::level_from_env());
    }

    // Select the graphics protocol once — hard gate. The single selected protocol
    // is shared (Arc) by the listener loop, catch-up, and the reader thread, so
    // the DA1/sixel probe runs exactly once at startup.
    let proto = match graphics::select() {
        Some(p) => Arc::new(p),
        None => {
            eprintln!(
                "laterm: terminal supports none of the kitty graphics, iTerm2 (imgcat), or sixel protocols; run inside a compatible terminal (kitty, ghostty, iTerm2, WezTerm, or a sixel-capable terminal)"
            );
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

    // Derive the working directory once; the log dir, the rendezvous socket, and
    // the live-payload cwd filter all key off it.
    let cwd = match std::env::current_dir() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("laterm: getwd: {e}");
            return 1;
        }
    };
    let home = match ipc::home_dir() {
        Some(h) => h,
        None => {
            eprintln!("laterm: home dir: not found");
            return 1;
        }
    };
    let dir = project_log_dir(&cwd, &home);
    let socket = match ipc::socket_path(&cwd) {
        Ok(p) => p,
        Err(e) => {
            eprintln!("laterm: cannot derive socket path: {e}");
            return 1;
        }
    };
    let watched_cwd = cwd.to_string_lossy().into_owned();
    logging::info(&format!("watching {}", dir.display()));
    logging::info(&format!("socket {}", socket.display()));

    // Startup line so the window doesn't look dead — standard terminal color, no
    // role marker/SGR. main is allowed to write stdout. The dir is tilde-collapsed.
    println!(
        "laterm {} monitoring {}/",
        env!("CARGO_PKG_VERSION"),
        display_dir(&dir, Some(&home))
    );
    // The dir only gates `--catch-up`; the live listener runs regardless. Surface
    // its absence as a not-yet signal, noting paste still works.
    if !dir.exists() {
        println!(
            "  no conversation log for this directory yet — paste to render, or start Claude Code here"
        );
    }

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

    // Build the render context once: renders the reference "X" to derive the block
    // threshold (1.5×) and proportional row sizing, and carries the proto.
    let ctx = Arc::new(RenderCtx::new(proto));

    // Stdout lock shared between the listener loop and the stdin thread.
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

    // Replay recent history before the live path begins. Catch-up reads completed
    // transcripts read-only and never touches the socket, so live hook payloads
    // (which arrive by push, not by tailing) are never double-emitted.
    if let Some(minutes) = args.catch_up {
        catch_up(&dir, minutes, &out_mu, &ctx);
    }

    // Live path: the ipc listener yields one raw hook payload per connection.
    let rx = match ipc::listen(&socket, shutdown.clone()) {
        Ok(rx) => rx,
        Err(e) => {
            eprintln!("laterm: cannot bind socket {}: {e}", socket.display());
            return 1;
        }
    };
    logging::info("listening for hook payloads");

    for payload in rx {
        if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
            break;
        }
        // hook::parse drops payloads for another project (cwd mismatch),
        // unrecognized events, empty content, and parse failures.
        let Some(parsed) = hook::parse(&payload, &watched_cwd) else {
            continue;
        };
        // Map the neutral parser role to feed's EntryStyle (the only place this
        // mapping lives; hook stays free of any feed import).
        let style = match parsed.role {
            hook::Role::User => &feed::USER_STYLE,
            hook::Role::Assistant => &feed::ASSISTANT_STYLE,
        };
        let _lock = out_mu.lock().unwrap();
        feed::emit_entry(style, &[&parsed.text], &ctx);
    }

    // Remove the socket file on exit (the accept loop also removes it; both ignore
    // errors, so a double remove is harmless).
    let _ = std::fs::remove_file(&socket);
    0
}

/// Merge laterm's two hook entries into an existing `settings.json` value,
/// additively and idempotently. `exe` is the absolute laterm path used in the
/// command. Existing top-level keys and existing hook entries are preserved; a
/// laterm entry is not duplicated on re-run. Pure (operates on a `serde_json`
/// value) so it is unit-testable without touching a real settings file.
fn merge_hooks(settings: Value, exe: &str) -> Value {
    let mut root = match settings {
        Value::Object(m) => m,
        _ => serde_json::Map::new(),
    };

    let hooks_val = root
        .entry("hooks")
        .or_insert_with(|| Value::Object(serde_json::Map::new()));
    let hooks = match hooks_val {
        Value::Object(m) => m,
        other => {
            *other = Value::Object(serde_json::Map::new());
            other.as_object_mut().expect("just set to object")
        }
    };

    for event in ["UserPromptSubmit", "Stop"] {
        let command = format!("{exe} --hook {event}");
        let arr_val = hooks
            .entry(event)
            .or_insert_with(|| Value::Array(Vec::new()));
        let arr = match arr_val {
            Value::Array(a) => a,
            other => {
                *other = Value::Array(Vec::new());
                other.as_array_mut().expect("just set to array")
            }
        };
        if !arr.iter().any(|e| entry_has_command(e, &command)) {
            arr.push(json!({ "hooks": [ { "type": "command", "command": command } ] }));
        }
    }

    Value::Object(root)
}

/// Whether a `hooks.<Event>[]` element already carries `command` (the
/// idempotency check for [`merge_hooks`]).
fn entry_has_command(entry: &Value, command: &str) -> bool {
    entry
        .get("hooks")
        .and_then(Value::as_array)
        .is_some_and(|inner| {
            inner
                .iter()
                .any(|h| h.get("command").and_then(Value::as_str) == Some(command))
        })
}

/// `--install-hooks`: merge the two hook entries into the target settings.json
/// and exit. Reads any existing file (a malformed one is an error, not clobbered),
/// merges additively + idempotently, and writes it back pretty-printed.
fn install_hooks(target: HookTarget) -> i32 {
    let exe = match std::env::current_exe() {
        Ok(p) => p.to_string_lossy().into_owned(),
        Err(e) => {
            eprintln!("laterm: cannot determine executable path: {e}");
            return 1;
        }
    };

    let settings_path = match target {
        HookTarget::Project => PathBuf::from(".claude").join("settings.json"),
        HookTarget::Global => {
            let home = match ipc::home_dir() {
                Some(h) => h,
                None => {
                    eprintln!("laterm: home dir: not found");
                    return 1;
                }
            };
            home.join(".claude").join("settings.json")
        }
    };

    let existing = match std::fs::read(&settings_path) {
        Ok(bytes) => match serde_json::from_slice::<Value>(&bytes) {
            Ok(v) => v,
            Err(e) => {
                eprintln!(
                    "laterm: {} is not valid JSON (refusing to clobber): {e}",
                    settings_path.display()
                );
                return 1;
            }
        },
        Err(ref e) if e.kind() == std::io::ErrorKind::NotFound => json!({}),
        Err(e) => {
            eprintln!("laterm: cannot read {}: {e}", settings_path.display());
            return 1;
        }
    };

    let merged = merge_hooks(existing, &exe);

    if let Some(parent) = settings_path.parent()
        && !parent.as_os_str().is_empty()
        && let Err(e) = std::fs::create_dir_all(parent)
    {
        eprintln!("laterm: cannot create {}: {e}", parent.display());
        return 1;
    }

    let mut out = match serde_json::to_string_pretty(&merged) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("laterm: cannot serialize settings: {e}");
            return 1;
        }
    };
    out.push('\n');
    if let Err(e) = std::fs::write(&settings_path, out) {
        eprintln!("laterm: cannot write {}: {e}", settings_path.display());
        return 1;
    }

    println!(
        "laterm: installed UserPromptSubmit and Stop hooks in {}",
        settings_path.display()
    );
    0
}

/// Render math from conversation entries timestamped within the last `minutes`,
/// across every *.jsonl file in `dir`, in timestamp order. Runs on the main
/// thread before the live listener starts.
///
/// Each replayed entry is emitted with a single `emit_entry` call covering all
/// its text segments — one marker-pair per entry — so caught-up framing is
/// byte-identical to the live hook path (which also emits once per entry).
fn catch_up(dir: &Path, minutes: i64, out_mu: &Mutex<()>, ctx: &RenderCtx) {
    let cutoff = Utc::now() - chrono::Duration::minutes(minutes);

    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(e) => {
            logging::warn(&format!("catch-up: cannot read {}: {e}", dir.display()));
            return;
        }
    };

    // One element per source jsonl entry, keeping its segments grouped so the emit
    // framing matches the live path (one marker-pair per entry).
    let mut recent: Vec<(DateTime<Utc>, Vec<convo::Segment>)> = Vec::new();
    for entry in entries.flatten() {
        let path = entry.path();
        if !convo::is_jsonl(&path) {
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
/// background. When true (sixel) the background is opaque: the detected terminal
/// color (so the sixel box blends in) or white when undetected.
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

/// Derive the Claude Code conversation-log directory for `cwd`:
/// `~/.claude/projects/<cwd with '/', '\', and ':' replaced by '-'>`. The
/// dash-mangling is the single shared helper `ipc` uses for the socket filename,
/// so the log dir and the rendezvous socket agree by construction.
fn project_log_dir(cwd: &Path, home: &Path) -> PathBuf {
    let name = ipc::mangle_dir(cwd);
    home.join(".claude").join("projects").join(name)
}

/// Render `dir` for the startup line: if it is under `home`, collapse that prefix
/// to `~`; otherwise show it unchanged. Uses the same home source as
/// [`project_log_dir`] so the collapse is consistent. Pure for unit testing.
fn display_dir(dir: &Path, home: Option<&Path>) -> String {
    if let Some(home) = home
        && let Ok(rest) = dir.strip_prefix(home)
    {
        return format!("~/{}", rest.display());
    }
    dir.display().to_string()
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
        assert!(a.cwd.is_none());
        assert!(a.log.is_none());
        assert!(a.catch_up.is_none());
    }

    #[test]
    fn cwd_short_long_and_equals_forms() {
        let short = run_args(argv(&["-C", "/tmp"]));
        assert_eq!(short.cwd, Some(PathBuf::from("/tmp")));
        let long = run_args(argv(&["--cwd", "/tmp"]));
        assert_eq!(long.cwd, Some(PathBuf::from("/tmp")));
        let eq = run_args(argv(&["--cwd=/tmp"]));
        assert_eq!(eq.cwd, Some(PathBuf::from("/tmp")));
    }

    #[test]
    fn cwd_missing_value_errors() {
        assert!(parse_args(argv(&["-C"])).is_err());
        assert!(parse_args(argv(&["--cwd"])).is_err());
        assert!(parse_args(argv(&["--cwd="])).is_err());
    }

    #[test]
    fn hook_requires_event() {
        assert!(matches!(
            parse_args(argv(&["--hook", "Stop"])),
            Ok(Invocation::Hook(e)) if e == "Stop"
        ));
        assert!(matches!(
            parse_args(argv(&["--hook=UserPromptSubmit"])),
            Ok(Invocation::Hook(e)) if e == "UserPromptSubmit"
        ));
        assert!(parse_args(argv(&["--hook"])).is_err());
        assert!(parse_args(argv(&["--hook", ""])).is_err());
        assert!(parse_args(argv(&["--hook="])).is_err());
    }

    #[test]
    fn install_hooks_default_and_targets() {
        assert!(matches!(
            parse_args(argv(&["--install-hooks"])),
            Ok(Invocation::InstallHooks(HookTarget::Project))
        ));
        assert!(matches!(
            parse_args(argv(&["--install-hooks", "--project"])),
            Ok(Invocation::InstallHooks(HookTarget::Project))
        ));
        assert!(matches!(
            parse_args(argv(&["--install-hooks", "--global"])),
            Ok(Invocation::InstallHooks(HookTarget::Global))
        ));
        // A target flag without --install-hooks is an error.
        assert!(parse_args(argv(&["--global"])).is_err());
    }

    #[test]
    fn display_dir_collapses_under_home() {
        let home = PathBuf::from("/Users/benn");
        let dir = PathBuf::from("/Users/benn/.claude/projects/-Users-benn-projects-laterm");
        assert_eq!(
            display_dir(&dir, Some(&home)),
            "~/.claude/projects/-Users-benn-projects-laterm"
        );
    }

    #[test]
    fn display_dir_passes_through_outside_home() {
        let home = PathBuf::from("/Users/benn");
        let dir = PathBuf::from("/var/data/-x");
        assert_eq!(display_dir(&dir, Some(&home)), "/var/data/-x");
        assert_eq!(display_dir(&dir, None), "/var/data/-x");
    }

    #[test]
    fn project_log_dir_uses_shared_mangling() {
        let cwd = PathBuf::from("/Users/benn/projects/laterm");
        let home = PathBuf::from("/Users/benn");
        let dir = project_log_dir(&cwd, &home);
        // The dir name is exactly the shared socket-mangling of the cwd, so the
        // log dir and the rendezvous socket agree by construction.
        assert_eq!(
            dir.file_name().unwrap().to_str().unwrap(),
            ipc::mangle_dir(&cwd)
        );
        assert_eq!(
            dir,
            home.join(".claude")
                .join("projects")
                .join("-Users-benn-projects-laterm")
        );
    }

    #[test]
    fn help_returns_help_variant() {
        assert!(matches!(
            parse_args(argv(&["--help"])),
            Ok(Invocation::Help)
        ));
        assert!(matches!(parse_args(argv(&["-h"])), Ok(Invocation::Help)));
    }

    #[test]
    fn version_returns_version_variant() {
        assert!(matches!(
            parse_args(argv(&["--version"])),
            Ok(Invocation::Version)
        ));
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

    #[test]
    fn merge_hooks_adds_both_and_is_idempotent() {
        let exe = "/abs/laterm";
        let once = merge_hooks(json!({}), exe);

        let ups = once["hooks"]["UserPromptSubmit"].as_array().unwrap();
        assert_eq!(ups.len(), 1);
        assert_eq!(
            ups[0]["hooks"][0]["command"],
            json!(format!("{exe} --hook UserPromptSubmit"))
        );
        assert_eq!(ups[0]["hooks"][0]["type"], json!("command"));
        let stop = once["hooks"]["Stop"].as_array().unwrap();
        assert_eq!(stop.len(), 1);
        assert_eq!(
            stop[0]["hooks"][0]["command"],
            json!(format!("{exe} --hook Stop"))
        );

        // Re-running does not duplicate.
        let twice = merge_hooks(once.clone(), exe);
        assert_eq!(twice, once);
    }

    #[test]
    fn merge_hooks_preserves_unrelated_settings_and_events() {
        let exe = "/abs/laterm";
        let existing = json!({
            "model": "sonnet",
            "hooks": {
                "PreToolUse": [ { "hooks": [ { "type": "command", "command": "other" } ] } ],
                "Stop": [ { "hooks": [ { "type": "command", "command": "pre-existing" } ] } ]
            }
        });
        let merged = merge_hooks(existing, exe);

        // Unrelated top-level key preserved.
        assert_eq!(merged["model"], json!("sonnet"));
        // Unrelated hook event preserved.
        assert_eq!(
            merged["hooks"]["PreToolUse"][0]["hooks"][0]["command"],
            json!("other")
        );
        // laterm's Stop entry is appended alongside the pre-existing one, not
        // clobbering it.
        let stop = merged["hooks"]["Stop"].as_array().unwrap();
        assert_eq!(stop.len(), 2);
        assert_eq!(stop[0]["hooks"][0]["command"], json!("pre-existing"));
        assert_eq!(
            stop[1]["hooks"][0]["command"],
            json!(format!("{exe} --hook Stop"))
        );
        // UserPromptSubmit is added fresh.
        assert_eq!(
            merged["hooks"]["UserPromptSubmit"]
                .as_array()
                .unwrap()
                .len(),
            1
        );
    }
}
