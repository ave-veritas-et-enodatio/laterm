// input: manual paste-only stdin handling for the viewing window.
//
// Reads stdin in raw mode (echo off). Text pasted between the bracketed-paste
// markers is rendered through the same path as a conversation entry; ordinary
// typing is rejected with a throttled BEL; Enter draws a manual separator rule.
// This module writes to stdout (separator rule, BEL, bracketed-paste toggles)
// and the pasted-entry render — always under the caller's shared output mutex.

use std::io::Write;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use crate::feed::{self, RenderCtx, PASTE_STYLE};
use crate::{logging, termbg};

/// Enable bracketed paste (only the feed/input modules write stdout).
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

/// ANSI reset (SGR 0) closing the separator color span.
const SGR_RESET: &str = "\x1b[0m";
/// Manual-separator rule: bold yellow, distinct from the role-marker colors.
const SEPARATOR_SGR: &str = "\x1b[1;33m";
/// Separator glyph: U+2550 box-drawings double horizontal (solid double line).
const SEPARATOR_CHAR: &str = "═";

/// Read manual input PASTE-ONLY: pasted text is captured silently (terminal
/// echo is off) and rendered through the same path as a conversation entry;
/// ordinary typing is ignored with a throttled BEL. Escape sequences (arrow
/// keys, etc.) and control bytes are consumed silently. The protocol and sizing
/// come from the shared [`RenderCtx`] (selected once in `main`).
pub(crate) fn read_input(out_mu: &Mutex<()>, ctx: &RenderCtx, shutdown: &AtomicBool) {
    let mut raw = match termbg::raw_input() {
        Some(r) => r,
        None => {
            logging::warn("read_input: could not enter raw stdin mode; manual paste input disabled");
            return;
        }
    };
    logging::info("read_input: raw stdin mode active; paste-only input enabled");

    {
        let _lock = out_mu.lock().unwrap();
        enable_bracketed_paste();
    }

    let mut parser = PasteParser::new();
    let mut last_beep: Option<Instant> = None;
    let mut buf = [0u8; 4096];

    loop {
        if shutdown.load(Ordering::Relaxed) {
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
                    feed::emit_entry(&PASTE_STYLE, &[&s], ctx);
                }
                Some(PasteEvent::Newline) => {
                    let _lock = out_mu.lock().unwrap();
                    // Enter inserts a deliberate separator (rule line). Refuse
                    // to stack a second one in a row — beep instead.
                    if feed::arm_separator() {
                        throttled_beep(&mut last_beep);
                    } else {
                        write_separator();
                    }
                }
                Some(PasteEvent::RejectTyping) => {
                    let _lock = out_mu.lock().unwrap();
                    throttled_beep(&mut last_beep);
                }
                None => {}
            }
        }
    }

    let _lock = out_mu.lock().unwrap();
    disable_bracketed_paste();
}

/// Emit one BEL, at most once per `BEEP_THROTTLE`, to avoid machine-gunning the
/// bell on a held key. Caller holds the output mutex.
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

#[cfg(test)]
mod tests {
    use super::*;

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
}
