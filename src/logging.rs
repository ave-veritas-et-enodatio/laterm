// logging: structured file logger for laterm diagnostics.
//
// Initialised once by logging::init(path, level). The file is opened for
// append (UNIX: mode 0600). When init is not called, or the file cannot be
// opened, all log calls are silent. The minimum level is supplied by the
// caller, which reads LATERM_LOG_LEVEL (default: info).

use std::fs::OpenOptions;
use std::io::Write;
use std::path::Path;
use std::sync::{Mutex, OnceLock};
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum Level {
    Debug = 0,
    Info  = 1,
    Warn  = 2,
    Error = 3,
}

impl Level {
    fn as_str(self) -> &'static str {
        match self {
            Level::Debug => "DEBUG",
            Level::Info  => "INFO",
            Level::Warn  => "WARN",
            Level::Error => "ERROR",
        }
    }
}

struct Logger {
    file:      Mutex<std::fs::File>,
    min_level: Level,
}

static LOGGER: OnceLock<Logger> = OnceLock::new();

/// Minimum level from LATERM_LOG_LEVEL (default: info).
pub fn level_from_env() -> Level {
    match std::env::var("LATERM_LOG_LEVEL")
        .unwrap_or_default()
        .to_ascii_lowercase()
        .as_str()
    {
        "debug" => Level::Debug,
        "warn"  => Level::Warn,
        "error" => Level::Error,
        _       => Level::Info,
    }
}

/// Call once at process start with the resolved log path and minimum level.
/// On open failure, prints one warning to stderr and leaves logging disabled.
pub fn init(path: &Path, min_level: Level) {
    match open_log_file(path) {
        Ok(f) => {
            let _ = LOGGER.set(Logger { file: Mutex::new(f), min_level });
        }
        Err(e) => {
            eprintln!("laterm: could not open log file {}: {e}", path.display());
        }
    }
}

/// Open the log file for append, with an EXCLUSIVE-writer claim so two laterm
/// instances can't clobber one shared file. Read-only access (e.g. `tail -f`)
/// is still allowed. A second writer fails here; `init` reports it and leaves
/// logging disabled for that instance.
fn open_log_file(path: &Path) -> std::io::Result<std::fs::File> {
    let mut opts = OpenOptions::new();
    opts.create(true).append(true);

    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    #[cfg(windows)]
    {
        // FILE_SHARE_READ: other processes may open for reading, not writing.
        use std::os::windows::fs::OpenOptionsExt;
        use windows_sys::Win32::Storage::FileSystem::FILE_SHARE_READ;
        opts.share_mode(FILE_SHARE_READ);
    }

    let file = opts.open(path)?;

    #[cfg(unix)]
    {
        use std::os::unix::io::AsRawFd;
        // Advisory exclusive lock, non-blocking: a second laterm fails fast.
        // Readers don't flock, so tailing the log is unaffected. Released when
        // the file (and the process) closes.
        // SAFETY: `file` owns a live, open fd for the duration of this call;
        // flock only operates on that descriptor and we check its return value.
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } != 0 {
            return Err(std::io::Error::new(
                std::io::ErrorKind::WouldBlock,
                "already held by another laterm instance",
            ));
        }
    }

    Ok(file)
}

/// Write a log line if the logger is initialised and the level passes.
pub fn log(level: Level, msg: &str) {
    let logger = match LOGGER.get() {
        Some(l) => l,
        None => return,
    };
    if level < logger.min_level {
        return;
    }

    // Simple seconds-precision timestamp from UNIX epoch.
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    let line = format!("{secs} {} {msg}\n", level.as_str());

    if let Ok(mut f) = logger.file.lock() {
        let _ = f.write_all(line.as_bytes());
    }
}

// Full level set; `debug`/`error` round out the API and may be unused today.
#[allow(dead_code)]
pub fn debug(msg: &str) { log(Level::Debug, msg); }
pub fn info(msg: &str)  { log(Level::Info,  msg); }
pub fn warn(msg: &str)  { log(Level::Warn,  msg); }
#[allow(dead_code)]
pub fn error(msg: &str) { log(Level::Error, msg); }

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[test]
    fn second_writer_is_locked_out() {
        let path = std::env::temp_dir().join(format!("laterm-lock-{}.log", std::process::id()));
        let _ = std::fs::remove_file(&path);

        let first = open_log_file(&path).expect("first open should succeed");
        assert!(
            open_log_file(&path).is_err(),
            "a second writer must fail while the first holds the lock"
        );

        drop(first); // releasing the fd releases the flock
        assert!(
            open_log_file(&path).is_ok(),
            "open should succeed once the lock is released"
        );

        let _ = std::fs::remove_file(&path);
    }
}
