// ipc: Unix-domain-socket transport between the `--hook` forwarder and the
// running renderer.
//
// Three pieces: (1) socket-path derivation shared with main's log-dir mangling
// so the client and the listener rendezvous on the same socket; (2) the listener
// accept-loop (the live producer, replacing the old file watcher); (3) the
// `--hook` send client. No other laterm module is imported (isolation invariant);
// std only, no new crate.
//
// Platform-split like `termbg`: unix uses `std::os::unix::net` now. The Windows
// named-pipe transport is a documented follow-up — on Windows the live-hook path
// is absent (cfg-stubbed) but the code still compiles, so paste + catch-up keep
// working there.

use std::path::{Path, PathBuf};

/// Dash-mangle an absolute working-directory path into the single canonical
/// string used for BOTH the Claude Code log-dir name and the socket filename, so
/// the `--hook` client and the renderer's listener derive the same socket.
///
/// Rule: every character NOT in `[A-Za-z0-9]` maps 1:1 to `-` (no collapsing). This
/// must track Claude Code's own project-dir naming convention byte-for-byte — e.g.
/// `/Users/benn/projects/agent_convos/ai-race` →
/// `-Users-benn-projects-agent-convos-ai-race` (note the `_` becomes `-`) and, on
/// Windows, `C:\Users\benn\projects\laterm` → `C--Users-benn-projects-laterm`. If
/// Claude Code ever diverges (e.g. unicode/edge chars), revisit this. It is the ONE
/// shared mangling helper — main's `project_log_dir` calls it too; do not fork it.
pub(crate) fn mangle_dir(cwd: &Path) -> String {
    cwd.to_string_lossy()
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '-' })
        .collect()
}

/// Home directory (HOME on unix; USERPROFILE, else HOME, on Windows). Shared by
/// main's log-dir derivation and the socket base below; it lives here so the
/// import-free `ipc` module and `main` use one implementation.
pub(crate) fn home_dir() -> Option<PathBuf> {
    #[cfg(unix)]
    {
        std::env::var_os("HOME").map(PathBuf::from)
    }
    #[cfg(windows)]
    {
        std::env::var_os("USERPROFILE")
            .or_else(|| std::env::var_os("HOME"))
            .map(PathBuf::from)
    }
}

/// Directory holding laterm's per-project sockets: `$XDG_RUNTIME_DIR` if set and
/// non-empty, else `~/.cache/laterm`.
fn socket_base() -> std::io::Result<PathBuf> {
    if let Some(rt) = std::env::var_os("XDG_RUNTIME_DIR")
        && !rt.is_empty()
    {
        return Ok(PathBuf::from(rt));
    }
    let home = home_dir().ok_or_else(|| {
        std::io::Error::new(std::io::ErrorKind::NotFound, "home directory not found")
    })?;
    Ok(home.join(".cache").join("laterm"))
}

/// Socket path for a given `base` and `cwd` — a pure join, no I/O. Split out from
/// [`socket_path`] so its determinism is unit-testable without touching the
/// filesystem.
fn socket_file(base: &Path, cwd: &Path) -> PathBuf {
    base.join(format!("{}.sock", mangle_dir(cwd)))
}

/// Socket path for working directory `cwd`. Deterministic (same cwd -> same path)
/// and shared by the listener and the `--hook` client so they rendezvous. Creates
/// the parent directory if needed.
pub(crate) fn socket_path(cwd: &Path) -> std::io::Result<PathBuf> {
    let base = socket_base()?;
    std::fs::create_dir_all(&base)?;
    Ok(socket_file(&base, cwd))
}

/// The `--hook` forwarder: derive the socket for `cwd` and send `bytes`. Any
/// failure returns Err so `main` exits 0 silently (never blocks Claude Code).
/// Writes nothing to stdout.
pub(crate) fn forward(cwd: &Path, bytes: &[u8]) -> std::io::Result<()> {
    let path = socket_path(cwd)?;
    send(&path, bytes)
}

#[cfg(unix)]
mod platform {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::{UnixListener, UnixStream};
    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::mpsc::{self, Receiver, Sender};
    use std::time::Duration;

    /// How often the accept loop wakes to observe the shutdown flag.
    const ACCEPT_POLL: Duration = Duration::from_millis(50);

    /// Bind the listener at `socket_path` (removing a stale socket file first) and
    /// spawn the accept loop. Returns a receiver yielding one raw payload per
    /// connection, in arrival order. The loop stops when `shutdown` is set and
    /// removes the socket file on the way out.
    pub(crate) fn listen(
        socket_path: &Path,
        shutdown: Arc<AtomicBool>,
    ) -> std::io::Result<Receiver<Vec<u8>>> {
        // A stale file from a prior crash would make bind fail with EADDRINUSE.
        let _ = std::fs::remove_file(socket_path);
        let listener = UnixListener::bind(socket_path)?;
        // Non-blocking accept so the loop can poll `shutdown`; a blocking accept
        // would strand a clean SIGINT shutdown until the next connection.
        listener.set_nonblocking(true)?;

        // Unbounded channel: the accept loop must never block on a slow renderer
        // (the client returns as soon as its bytes are written).
        let (tx, rx) = mpsc::channel::<Vec<u8>>();
        let path = socket_path.to_path_buf();
        std::thread::spawn(move || accept_loop(listener, path, shutdown, tx));
        Ok(rx)
    }

    fn accept_loop(
        listener: UnixListener,
        path: PathBuf,
        shutdown: Arc<AtomicBool>,
        tx: Sender<Vec<u8>>,
    ) {
        while !shutdown.load(Ordering::Relaxed) {
            match listener.accept() {
                Ok((stream, _addr)) => {
                    if let Some(payload) = read_payload(stream)
                        && tx.send(payload).is_err()
                    {
                        break; // renderer dropped the receiver
                    }
                }
                Err(ref e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                    std::thread::sleep(ACCEPT_POLL);
                }
                // Any other accept error: stop producing (the old watcher likewise
                // returned silently on a fatal producer error).
                Err(_) => break,
            }
        }
        let _ = std::fs::remove_file(&path);
    }

    /// Read one connection to EOF — one raw payload per connection. Returns None on
    /// a read error or an empty payload.
    fn read_payload(mut stream: UnixStream) -> Option<Vec<u8>> {
        // An accepted stream may inherit the listener's non-blocking flag on some
        // platforms; force blocking so `read_to_end` reliably reaches EOF.
        stream.set_nonblocking(false).ok()?;
        let mut buf = Vec::new();
        match stream.read_to_end(&mut buf) {
            Ok(_) if !buf.is_empty() => Some(buf),
            _ => None,
        }
    }

    /// Connect to `socket_path` and write `bytes`. On any failure (no listener,
    /// refused, gone) returns Err so the `--hook` caller exits 0 silently.
    pub(crate) fn send(socket_path: &Path, bytes: &[u8]) -> std::io::Result<()> {
        let mut stream = UnixStream::connect(socket_path)?;
        stream.write_all(bytes)?;
        stream.flush()
    }
}

#[cfg(not(unix))]
mod platform {
    use super::*;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::mpsc::{self, Receiver};
    use std::time::Duration;

    /// How often the placeholder producer wakes to observe the shutdown flag.
    const SHUTDOWN_POLL: Duration = Duration::from_millis(100);

    /// Windows: the Unix-domain-socket live-hook transport is unsupported; the
    /// named-pipe transport is a planned follow-up. Return a receiver that yields
    /// nothing and closes when `shutdown` is set, so the renderer still serves
    /// paste + catch-up and stays alive until SIGINT/close.
    pub(crate) fn listen(
        _socket_path: &Path,
        shutdown: Arc<AtomicBool>,
    ) -> std::io::Result<Receiver<Vec<u8>>> {
        let (tx, rx) = mpsc::channel::<Vec<u8>>();
        std::thread::spawn(move || {
            while !shutdown.load(Ordering::Relaxed) {
                std::thread::sleep(SHUTDOWN_POLL);
            }
            drop(tx);
        });
        Ok(rx)
    }

    /// Windows: no UDS, so the `--hook` forwarder is a silent no-op until the
    /// named-pipe transport lands. The caller exits 0 regardless.
    pub(crate) fn send(_socket_path: &Path, _bytes: &[u8]) -> std::io::Result<()> {
        Ok(())
    }
}

pub(crate) use platform::{listen, send};

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn socket_file_deterministic_and_shares_mangling() {
        let base = Path::new("/run/user/1000");
        let cwd = Path::new("/Users/benn/projects/laterm");
        let a = socket_file(base, cwd);
        let b = socket_file(base, cwd);
        assert_eq!(a, b, "same base+cwd must yield the same socket path");
        // The filename is the shared dash-mangled cwd + .sock — the SAME mangling
        // the log-dir derivation uses (rendezvous depends on this agreement).
        assert_eq!(
            a.file_name().unwrap().to_str().unwrap(),
            format!("{}.sock", mangle_dir(cwd))
        );
    }

    #[test]
    fn mangle_dir_replaces_separators() {
        assert_eq!(
            mangle_dir(Path::new("/Users/benn/projects/laterm")),
            "-Users-benn-projects-laterm"
        );
        // Windows-style drive + backslashes map the same way (`:` and `\` -> `-`).
        assert_eq!(mangle_dir(Path::new(r"C:\Users\benn")), "C--Users-benn");
        // Every non-alphanumeric char maps to `-`, including `_` — this must match
        // Claude Code's project-dir naming (the catch-up log dir depends on it).
        assert_eq!(
            mangle_dir(Path::new("/Users/benn/projects/agent_convos/ai-race")),
            "-Users-benn-projects-agent-convos-ai-race"
        );
    }
}

// Live Unix-domain-socket I/O: bind -> connect -> send -> receive. Hermetic —
// temp-dir socket paths only, never ~/.cache or ~/.claude. Unix-only (the UDS
// transport does not exist on Windows).
#[cfg(all(test, unix))]
mod uds_tests {
    use super::*;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::time::{Duration, Instant};

    /// A per-test socket path under the temp dir, unique to this process (no
    /// randomness source is available, so the pid + a tag suffices).
    fn unique_socket(tag: &str) -> PathBuf {
        std::env::temp_dir().join(format!("laterm-ipc-test-{}-{tag}.sock", std::process::id()))
    }

    /// Poll until `path` no longer exists or `timeout` elapses.
    fn wait_gone(path: &Path, timeout: Duration) -> bool {
        let start = Instant::now();
        while start.elapsed() < timeout {
            if !path.exists() {
                return true;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        !path.exists()
    }

    #[test]
    fn roundtrip_delivers_exact_bytes_and_cleans_up_on_shutdown() {
        let path = unique_socket("roundtrip");
        let _ = std::fs::remove_file(&path);

        let shutdown = Arc::new(AtomicBool::new(false));
        let rx = listen(&path, shutdown.clone()).expect("bind listener");

        let payload = b"hello over the socket";
        send(&path, payload).expect("send to a bound listener");

        // recv_timeout so a broken transport fails fast instead of hanging the suite.
        let got = rx
            .recv_timeout(Duration::from_secs(2))
            .expect("payload received within timeout");
        assert_eq!(got.as_slice(), payload);

        // Shutdown: the accept loop breaks and removes the socket file.
        shutdown.store(true, Ordering::Relaxed);
        assert!(
            wait_gone(&path, Duration::from_secs(2)),
            "socket file must be removed on shutdown"
        );
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn send_without_listener_errors() {
        let path = unique_socket("nolistener");
        let _ = std::fs::remove_file(&path);
        // No bound listener: connect is refused, so the `--hook` caller would exit
        // 0 silently.
        assert!(send(&path, b"x").is_err());
    }

    #[test]
    fn transport_and_parser_compose() {
        let cwd = "/Users/benn/projects/laterm";
        let path = unique_socket("e2e");
        let _ = std::fs::remove_file(&path);

        let shutdown = Arc::new(AtomicBool::new(false));
        let rx = listen(&path, shutdown.clone()).expect("bind listener");

        let payload = br#"{"hook_event_name":"Stop","cwd":"/Users/benn/projects/laterm","last_assistant_message":"the answer is 42"}"#;
        send(&path, payload).expect("send Stop payload");

        let got = rx
            .recv_timeout(Duration::from_secs(2))
            .expect("payload received within timeout");
        // Exactly how `main` wires the two: transport yields raw bytes, `hook`
        // parses them.
        let parsed = crate::hook::parse(&got, cwd).expect("parse the received payload");
        assert_eq!(parsed.role, crate::hook::Role::Assistant);
        assert_eq!(parsed.text, "the answer is 42");

        shutdown.store(true, Ordering::Relaxed);
        let _ = wait_gone(&path, Duration::from_secs(2));
        let _ = std::fs::remove_file(&path);
    }
}
