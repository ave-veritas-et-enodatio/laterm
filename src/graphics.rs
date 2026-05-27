// graphics: terminal image protocol selection and encoding.
// Supports the kitty graphics protocol, iTerm2 imgcat (OSC 1337), and SIXEL.
// kitty is preferred, then imgcat, then sixel.

use base64::{engine::general_purpose::STANDARD as BASE64, Engine as _};

use crate::sixel;

/// Maximum base64 payload per APC escape (kitty protocol requirement).
const CHUNK_SIZE: usize = 4096;

/// A selected terminal image protocol.
pub struct Protocol {
    pub name: &'static str,
    encode_fn: fn(&[u8]) -> Vec<u8>,
    encode_inline_fn: fn(&[u8]) -> Vec<u8>,
}

impl Protocol {
    /// Encode PNG at native size (block use: own line).
    pub fn encode(&self, png: &[u8]) -> Vec<u8> {
        (self.encode_fn)(png)
    }

    /// Encode PNG scaled to one text row (inline use).
    pub fn encode_inline(&self, png: &[u8]) -> Vec<u8> {
        (self.encode_inline_fn)(png)
    }
}

/// Select the best available image protocol: kitty, then imgcat (both via
/// environment), then sixel (via a runtime DA1 tty round-trip, so it is probed
/// last). Returns None when none is supported.
pub fn select() -> Option<Protocol> {
    if kitty_supported() {
        return Some(Protocol {
            name: "kitty",
            encode_fn: kitty_encode,
            encode_inline_fn: kitty_encode_inline,
        });
    }
    if imgcat_supported() {
        return Some(Protocol {
            name: "imgcat",
            encode_fn: imgcat_encode,
            encode_inline_fn: imgcat_encode_inline,
        });
    }
    if sixel::supported() {
        return Some(Protocol {
            name: "sixel",
            encode_fn: sixel::encode,
            encode_inline_fn: sixel::encode_inline,
        });
    }
    None
}

// ---- kitty ----

fn kitty_supported() -> bool {
    kitty_supported_env(|k| std::env::var(k))
}

fn kitty_supported_env<F>(env: F) -> bool
where
    F: Fn(&str) -> Result<String, std::env::VarError>,
{
    if env("KITTY_WINDOW_ID").map(|v| !v.is_empty()).unwrap_or(false) {
        return true;
    }
    let term = env("TERM").unwrap_or_default();
    if term.contains("kitty") || term.contains("ghostty") {
        return true;
    }
    if env("TERM_PROGRAM").map(|v| v == "ghostty").unwrap_or(false) {
        return true;
    }
    false
}

fn kitty_encode(png: &[u8]) -> Vec<u8> {
    kitty_encode_impl(png, "")
}

fn kitty_encode_inline(png: &[u8]) -> Vec<u8> {
    kitty_encode_impl(png, ",r=1")
}

fn kitty_encode_impl(png: &[u8], size_args: &str) -> Vec<u8> {
    let b64 = BASE64.encode(png);
    let b64_bytes = b64.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(b64_bytes.len() + 64);
    let mut first = true;
    let mut remaining = b64_bytes;

    while !remaining.is_empty() {
        let n = remaining.len().min(CHUNK_SIZE);
        let chunk = &remaining[..n];
        remaining = &remaining[n..];

        let more = if remaining.is_empty() { 0u8 } else { 1u8 };

        out.extend_from_slice(b"\x1b_G");
        if first {
            out.extend_from_slice(format!("a=T,f=100{size_args},m={more}").as_bytes());
            first = false;
        } else {
            out.extend_from_slice(format!("m={more}").as_bytes());
        }
        out.push(b';');
        out.extend_from_slice(chunk);
        out.extend_from_slice(b"\x1b\\");
    }
    out
}

// ---- imgcat ----

fn imgcat_supported() -> bool {
    imgcat_supported_env(|k| std::env::var(k))
}

fn imgcat_supported_env<F>(env: F) -> bool
where
    F: Fn(&str) -> Result<String, std::env::VarError>,
{
    if env("TERM_PROGRAM").map(|v| v == "iTerm.app").unwrap_or(false) {
        return true;
    }
    if env("LC_TERMINAL").map(|v| v == "iTerm2").unwrap_or(false) {
        return true;
    }
    if env("TERM_PROGRAM").map(|v| v == "WezTerm").unwrap_or(false) {
        return true;
    }
    false
}

fn imgcat_encode(png: &[u8]) -> Vec<u8> {
    imgcat_encode_impl(png, "")
}

fn imgcat_encode_inline(png: &[u8]) -> Vec<u8> {
    imgcat_encode_impl(png, "height=1;preserveAspectRatio=1;")
}

fn imgcat_encode_impl(png: &[u8], extra_args: &str) -> Vec<u8> {
    let b64 = BASE64.encode(png);
    format!("\x1b]1337;File=inline=1;{extra_args}size={}:{b64}\x07", png.len()).into_bytes()
}

#[cfg(test)]
mod tests {
    use super::*;

    // ---- kitty tests ----

    fn env_map<'a>(pairs: &'a [(&'a str, &'a str)]) -> impl Fn(&str) -> Result<String, std::env::VarError> + 'a {
        move |k: &str| {
            pairs
                .iter()
                .find(|(key, _)| *key == k)
                .map(|(_, v)| v.to_string())
                .ok_or(std::env::VarError::NotPresent)
        }
    }

    #[test]
    fn kitty_supported_kitty_window_id() {
        assert!(kitty_supported_env(env_map(&[("KITTY_WINDOW_ID", "1")])));
    }

    #[test]
    fn kitty_supported_term_xterm_kitty() {
        assert!(kitty_supported_env(env_map(&[("TERM", "xterm-kitty")])));
    }

    #[test]
    fn kitty_supported_term_ghostty() {
        assert!(kitty_supported_env(env_map(&[("TERM", "xterm-ghostty")])));
    }

    #[test]
    fn kitty_supported_term_program_ghostty() {
        assert!(kitty_supported_env(env_map(&[("TERM_PROGRAM", "ghostty")])));
    }

    #[test]
    fn kitty_supported_plain_xterm() {
        assert!(!kitty_supported_env(env_map(&[("TERM", "xterm-256color")])));
    }

    #[test]
    fn kitty_supported_iterm() {
        assert!(!kitty_supported_env(env_map(&[("TERM_PROGRAM", "iTerm.app")])));
    }

    #[test]
    fn kitty_supported_empty() {
        assert!(!kitty_supported_env(env_map(&[])));
    }

    fn decode_kitty_payload(out: &[u8]) -> Vec<u8> {
        let s = std::str::from_utf8(out).unwrap();
        let mut b64 = String::new();
        for esc in s.split("\x1b\\") {
            if esc.is_empty() {
                continue;
            }
            // Strip leading ESC_G prefix (starts after \x1b_G)
            let body = esc.strip_prefix("\x1b_G").unwrap_or(esc);
            let semi = body.find(';').expect("escape missing ';'");
            b64.push_str(&body[semi + 1..]);
        }
        BASE64.decode(&b64).unwrap()
    }

    #[test]
    fn kitty_encode_structure_and_roundtrip() {
        // Larger than CHUNK_SIZE to exercise multi-chunk encoding.
        let payload: Vec<u8> = (0..CHUNK_SIZE * 2 + 17).map(|i| i as u8).collect();
        let out = kitty_encode(&payload);
        let s = std::str::from_utf8(&out).unwrap();
        assert!(s.starts_with("\x1b_Ga=T,f=100,m="), "output does not start with graphics control prefix");
        assert!(s.ends_with("\x1b\\"), "output does not end with ST");
        assert!(s.contains("m=0"), "output missing final m=0 chunk");
        let decoded = decode_kitty_payload(&out);
        assert_eq!(decoded, payload, "round-trip payload mismatch");
    }

    #[test]
    fn kitty_encode_inline_has_row_arg() {
        let out = kitty_encode_inline(b"hello");
        let s = std::str::from_utf8(&out).unwrap();
        assert!(s.starts_with("\x1b_Ga=T,f=100,r=1,m="), "inline output missing r=1 size arg: {s:?}");
    }

    // ---- imgcat tests ----

    #[test]
    fn imgcat_supported_iterm_app() {
        assert!(imgcat_supported_env(env_map(&[("TERM_PROGRAM", "iTerm.app")])));
    }

    #[test]
    fn imgcat_supported_lc_terminal_iterm2() {
        assert!(imgcat_supported_env(env_map(&[("LC_TERMINAL", "iTerm2")])));
    }

    #[test]
    fn imgcat_supported_wezterm() {
        assert!(imgcat_supported_env(env_map(&[("TERM_PROGRAM", "WezTerm")])));
    }

    #[test]
    fn imgcat_not_supported_unknown() {
        assert!(!imgcat_supported_env(env_map(&[])));
    }

    #[test]
    fn imgcat_not_supported_xterm() {
        assert!(!imgcat_supported_env(env_map(&[("TERM_PROGRAM", "xterm")])));
    }

    #[test]
    fn imgcat_encode_structure_and_roundtrip() {
        let input = b"fake-png-data-for-testing";
        let out = imgcat_encode(input);
        assert!(out.starts_with(b"\x1b]1337;File="), "output does not start with OSC 1337");
        assert_eq!(out.last(), Some(&b'\x07'), "output does not end with BEL");

        let size_tag = format!("size={}", input.len());
        let s = std::str::from_utf8(&out).unwrap();
        assert!(s.contains(&size_tag), "output missing {size_tag}");

        // Decode base64 after last ':' before BEL.
        let colon = s.rfind(':').expect("no ':' in output");
        let b64part = &s[colon + 1..s.len() - 1]; // strip BEL
        let decoded = BASE64.decode(b64part).unwrap();
        assert_eq!(decoded, input);
    }
}
