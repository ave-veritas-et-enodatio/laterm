// watch: poll a directory for *.jsonl files and emit newly-appended complete
// lines over a channel.
//
// Tail-only: existing files are recorded at their current size at startup;
// files appearing later start at offset 0. Missing dir is not fatal.
// The channel sender is dropped (and thus closed) when the shutdown flag fires.

use std::collections::HashMap;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::sync::mpsc::{self, Receiver, SyncSender};
use std::sync::Arc;
use std::time::Duration;

/// One complete JSONL entry (newline stripped) from a watched file.
#[allow(dead_code)]
pub struct Line {
    pub path: PathBuf,
    pub data: Vec<u8>,
}

struct FileState {
    offset: u64,
    pending: Vec<u8>,
}

/// Start a polling watcher on `dir` at `interval`.
/// Returns a Receiver that yields complete lines. The background thread stops
/// when `shutdown` is set; dropping the receiver also stops it eventually.
pub fn watch(
    dir: impl Into<PathBuf>,
    interval: Duration,
    shutdown: Arc<std::sync::atomic::AtomicBool>,
) -> Receiver<Line> {
    let dir = dir.into();
    let (tx, rx) = mpsc::sync_channel::<Line>(64);
    std::thread::spawn(move || poll(dir, interval, shutdown, tx));
    rx
}

fn poll(
    dir: PathBuf,
    interval: Duration,
    shutdown: Arc<std::sync::atomic::AtomicBool>,
    tx: SyncSender<Line>,
) {
    let mut state: HashMap<PathBuf, FileState> = HashMap::new();
    let mut initialized = false;

    loop {
        if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
            return;
        }

        tick(&dir, &mut state, &mut initialized, &tx, &shutdown);

        // Sleep in small increments so shutdown is responsive.
        let steps = (interval.as_millis() / 50).max(1) as u64;
        let step = interval / steps as u32;
        for _ in 0..steps {
            if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
                return;
            }
            std::thread::sleep(step);
        }
    }
}

fn tick(
    dir: &Path,
    state: &mut HashMap<PathBuf, FileState>,
    initialized: &mut bool,
    tx: &SyncSender<Line>,
    shutdown: &Arc<std::sync::atomic::AtomicBool>,
) {
    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(_) => return, // dir absent or unreadable — keep waiting
    };

    let mut jsonl_files: Vec<(PathBuf, u64)> = Vec::new();
    for entry in entries.flatten() {
        let name = entry.file_name();
        let name_str = name.to_string_lossy();
        if !name_str.ends_with(".jsonl") {
            continue;
        }
        let abs = dir.join(entry.file_name());
        let size = entry.metadata().map(|m| m.len()).unwrap_or(0);
        jsonl_files.push((abs, size));
    }

    if !*initialized {
        *initialized = true;
        for (abs, size) in &jsonl_files {
            state.insert(abs.clone(), FileState { offset: *size, pending: Vec::new() });
        }
        return; // nothing to emit on initialization tick
    }

    for (abs, size) in &jsonl_files {
        let fs = state.entry(abs.clone()).or_insert(FileState { offset: 0, pending: Vec::new() });

        if *size < fs.offset {
            // File shrunk (truncation / rotation): reset.
            fs.offset = 0;
            fs.pending.clear();
        }

        if *size == fs.offset {
            continue; // nothing new
        }

        read_file(abs, fs, tx, shutdown);
    }
}

fn read_file(
    path: &Path,
    fs: &mut FileState,
    tx: &SyncSender<Line>,
    shutdown: &Arc<std::sync::atomic::AtomicBool>,
) {
    let mut f = match std::fs::File::open(path) {
        Ok(f) => f,
        Err(_) => return,
    };

    if fs.offset > 0 && f.seek(SeekFrom::Start(fs.offset)).is_err() {
        return;
    }

    let mut new_bytes = Vec::new();
    if f.read_to_end(&mut new_bytes).is_err() || new_bytes.is_empty() {
        return;
    }

    // Advance offset past all newly-read bytes immediately to avoid re-reads.
    fs.offset += new_bytes.len() as u64;

    // Build working buffer: pending partial bytes + new file bytes.
    let mut buf = if fs.pending.is_empty() {
        new_bytes
    } else {
        let mut b = std::mem::take(&mut fs.pending);
        b.extend_from_slice(&new_bytes);
        b
    };

    // Split on '\n', emit complete lines.
    loop {
        match buf.iter().position(|&b| b == b'\n') {
            None => {
                // No complete line — stash remainder as pending.
                fs.pending = buf;
                return;
            }
            Some(idx) => {
                let mut line = buf[..idx].to_vec();
                if line.last() == Some(&b'\r') {
                    line.pop();
                }
                buf = buf[idx + 1..].to_vec();
                if line.is_empty() {
                    continue;
                }
                if shutdown.load(std::sync::atomic::Ordering::Relaxed) {
                    return;
                }
                // If send fails (receiver dropped), stop.
                if tx.send(Line { path: path.to_path_buf(), data: line }).is_err() {
                    return;
                }
            }
        }
    }
}
