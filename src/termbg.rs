// termbg: detect the terminal's background color via an OSC 11 query.
//
// Returns None on any failure (no tty, timeout, parse error). The caller
// falls back to a white background.
//
// parse_osc11 and is_dark are platform-independent so their unit tests run
// everywhere.

use std::time::Duration;

// ---- public interface ----

/// Query the terminal background color via OSC 11.
/// Returns Some((r, g, b)) on success, None on any failure.
pub fn query(timeout: Duration) -> Option<(u8, u8, u8)> {
    let reply = query_terminal(b"\x1b]11;?\x07", timeout)?;
    parse_osc11(&reply)
}

/// Send `request` to the controlling terminal in raw mode and return its reply
/// as a string. Platform-independent front door over the unix/Windows tty
/// machinery; used by both this module's OSC 11 query and sixel's DA1 query.
/// Returns None on any failure (no tty, timeout, non-UTF8 reply).
pub(crate) fn query_terminal(request: &[u8], timeout: Duration) -> Option<String> {
    platform::query(request, timeout)
}

/// Report whether an RGB color is dark (relative luminance < 128).
pub fn is_dark(r: u8, g: u8, b: u8) -> bool {
    let lum = 0.2126 * f64::from(r) + 0.7152 * f64::from(g) + 0.0722 * f64::from(b);
    lum < 128.0
}

// ---- shared parser (used by both platforms) ----

/// Parse an OSC 11 response like "\x1b]11;rgb:2e2e/3434/3636\x07".
/// Component fields may be 1–4 hex digits and are scaled to 8 bits.
pub(crate) fn parse_osc11(s: &str) -> Option<(u8, u8, u8)> {
    let i = s.find("rgb:")?;
    let rest = &s[i + 4..];
    // Trim at BEL or ESC.
    let rest = if let Some(j) = rest.find(['\x07', '\x1b']) {
        &rest[..j]
    } else {
        rest
    };
    let parts: Vec<&str> = rest.split('/').collect();
    if parts.len() != 3 {
        return None;
    }
    let r = scale_hex(parts[0])?;
    let g = scale_hex(parts[1])?;
    let b = scale_hex(parts[2])?;
    Some((r, g, b))
}

/// Parse a 1–4 digit hex color component and scale to 8 bits.
fn scale_hex(h: &str) -> Option<u8> {
    if h.is_empty() || h.len() > 4 {
        return None;
    }
    let v = u64::from_str_radix(h, 16).ok()?;
    let max = (1u64 << (4 * h.len())) - 1;
    Some((v * 255 / max) as u8)
}

// ---- unix implementation ----

#[cfg(unix)]
mod platform {
    use std::io::Write;
    use std::os::unix::io::{AsRawFd, RawFd};
    use std::time::Duration;

    pub fn query(request: &[u8], timeout: Duration) -> Option<String> {
        let mut tty = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open("/dev/tty")
            .ok()?;

        let fd = tty.as_raw_fd();

        if unsafe { libc::isatty(fd) } == 0 {
            return None;
        }

        let old = raw_mode_enable(fd)?;

        let result = (|| {
            tty.write_all(request).ok()?;
            tty.flush().ok()?;

            let mut buf = [0u8; 64];
            let n = read_with_timeout(fd, &mut buf, timeout)?;
            std::str::from_utf8(&buf[..n]).ok().map(str::to_string)
        })();

        raw_mode_disable(fd, &old);
        result
    }

    fn raw_mode_enable(fd: RawFd) -> Option<libc::termios> {
        unsafe {
            let mut old: libc::termios = std::mem::zeroed();
            if libc::tcgetattr(fd, &mut old) != 0 {
                return None;
            }
            let mut raw = old;
            libc::cfmakeraw(&mut raw);
            if libc::tcsetattr(fd, libc::TCSAFLUSH, &raw) != 0 {
                return None;
            }
            Some(old)
        }
    }

    fn raw_mode_disable(fd: RawFd, old: &libc::termios) {
        unsafe {
            libc::tcsetattr(fd, libc::TCSAFLUSH, old);
        }
    }

    fn read_with_timeout(fd: RawFd, buf: &mut [u8], timeout: Duration) -> Option<usize> {
        unsafe {
            let mut tv = libc::timeval {
                tv_sec:  timeout.as_secs() as libc::time_t,
                tv_usec: timeout.subsec_micros() as libc::suseconds_t,
            };
            let mut readfds: libc::fd_set = std::mem::zeroed();
            libc::FD_SET(fd, &mut readfds);
            let ret = libc::select(
                fd + 1,
                &mut readfds,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                &mut tv,
            );
            if ret <= 0 {
                return None;
            }
            let n = libc::read(fd, buf.as_mut_ptr() as *mut libc::c_void, buf.len());
            if n <= 0 { None } else { Some(n as usize) }
        }
    }
}

// ---- windows implementation ----

#[cfg(windows)]
mod platform {
    use std::sync::mpsc;
    use std::time::Duration;

    use windows_sys::Win32::Foundation::HANDLE;
    use windows_sys::Win32::System::Console::{
        GetConsoleMode, GetStdHandle, ReadConsoleA, SetConsoleMode,
        ENABLE_ECHO_INPUT, ENABLE_LINE_INPUT, ENABLE_PROCESSED_INPUT,
        ENABLE_VIRTUAL_TERMINAL_INPUT, ENABLE_VIRTUAL_TERMINAL_PROCESSING,
        STD_INPUT_HANDLE, STD_OUTPUT_HANDLE,
    };

    pub fn query(request: &[u8], timeout: Duration) -> Option<String> {
        // Run the blocking Windows API calls in a thread so they cannot hang
        // the main thread if WaitForSingleObject or ReadConsoleA stall
        // (e.g. under MINGW64 / Git Bash where STD_INPUT_HANDLE is a pipe).
        let request = request.to_vec();
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            let _ = tx.send(unsafe { do_query(&request) });
        });
        rx.recv_timeout(timeout).ok().flatten()
    }

    unsafe fn do_query(request: &[u8]) -> Option<String> {
        use std::io::Write;

        let conin: HANDLE = GetStdHandle(STD_INPUT_HANDLE);
        let hout:  HANDLE = GetStdHandle(STD_OUTPUT_HANDLE);

        if conin.is_null() || conin == usize::MAX as HANDLE as *mut _ {
            return None;
        }

        // Ensure output handle has VT processing enabled.
        let mut out_mode: u32 = 0;
        if GetConsoleMode(hout, &mut out_mode) != 0 {
            let _ = SetConsoleMode(hout, out_mode | ENABLE_VIRTUAL_TERMINAL_PROCESSING);
        }

        // Require a real console input handle; pipes (MINGW64 stdin) fail here.
        let mut old_mode: u32 = 0;
        if GetConsoleMode(conin, &mut old_mode) == 0 {
            return None;
        }

        let raw_mode = (old_mode
            & !(ENABLE_LINE_INPUT | ENABLE_ECHO_INPUT | ENABLE_PROCESSED_INPUT))
            | ENABLE_VIRTUAL_TERMINAL_INPUT;
        if SetConsoleMode(conin, raw_mode) == 0 {
            return None;
        }

        let result = (|| {
            let mut stdout = std::io::stdout();
            stdout.write_all(request).ok()?;
            stdout.flush().ok()?;

            let mut buf = [0u8; 64];
            let mut chars_read: u32 = 0;
            let ok = ReadConsoleA(
                conin,
                buf.as_mut_ptr() as *mut _,
                buf.len() as u32,
                &mut chars_read,
                std::ptr::null(),
            );
            if ok == 0 || chars_read == 0 {
                return None;
            }
            std::str::from_utf8(&buf[..chars_read as usize]).ok().map(str::to_string)
        })();

        SetConsoleMode(conin, old_mode);
        result
    }
}

// ---- tests (platform-independent) ----

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_osc11_16bit_bel() {
        assert_eq!(parse_osc11("\x1b]11;rgb:2e2e/3434/3636\x07"), Some((0x2e, 0x34, 0x36)));
    }

    #[test]
    fn parse_osc11_16bit_st() {
        assert_eq!(parse_osc11("\x1b]11;rgb:ffff/ffff/ffff\x1b\\"), Some((0xff, 0xff, 0xff)));
    }

    #[test]
    fn parse_osc11_black() {
        assert_eq!(parse_osc11("\x1b]11;rgb:0000/0000/0000\x07"), Some((0, 0, 0)));
    }

    #[test]
    fn parse_osc11_8bit_components() {
        assert_eq!(parse_osc11("\x1b]11;rgb:2e/34/36\x07"), Some((0x2e, 0x34, 0x36)));
    }

    #[test]
    fn parse_osc11_no_rgb_prefix() {
        assert_eq!(parse_osc11("\x1b]11;something\x07"), None);
    }

    #[test]
    fn parse_osc11_wrong_field_count() {
        assert_eq!(parse_osc11("\x1b]11;rgb:2e2e/3434\x07"), None);
    }

    #[test]
    fn parse_osc11_empty() {
        assert_eq!(parse_osc11(""), None);
    }

    #[test]
    fn is_dark_black() {
        assert!(is_dark(0, 0, 0));
    }

    #[test]
    fn is_dark_white() {
        assert!(!is_dark(0xff, 0xff, 0xff));
    }

    #[test]
    fn is_dark_dim_gray() {
        assert!(is_dark(0x2e, 0x2e, 0x2e));
    }

    #[test]
    fn is_dark_light_gray() {
        assert!(!is_dark(0xcc, 0xcc, 0xcc));
    }
}
