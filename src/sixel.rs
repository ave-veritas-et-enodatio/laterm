// sixel: render PNG bytes as a SIXEL escape sequence for terminals that
// support the SIXEL graphics protocol (notably Windows Terminal v1.22+) but
// not the kitty or iTerm2 (imgcat) protocols.
//
// Support is probed at runtime with a DA1 (Device Attributes) query rather
// than environment variables, reusing termbg's raw-tty machinery.

use std::collections::HashMap;
use std::sync::atomic::{AtomicU32, Ordering};
use std::time::Duration;

/// Timeout for the DA1 capability probe.
const DA1_TIMEOUT: Duration = Duration::from_millis(200);

/// Rows per SIXEL band. A sixel character encodes a vertical strip of 6 pixels,
/// so the encoder processes the image in 6-row bands and pads heights to a
/// multiple of this.
const SIXEL_BAND: usize = 6;

/// Base sixel data character: bit pattern 0 maps to '?' (0x3F); each set bit b
/// (row within the band) adds 1<<b. Output chars range 0x3F..=0x7E.
const SIXEL_CHAR_BASE: u8 = 0x3F;

/// SIXEL color-register components are on a 0..=100 scale (percent), unlike the
/// 0..=255 of the source pixels.
const COLOR_SCALE: u32 = 100;

/// Opaque background color used for alpha compositing, packed as 0x00RRGGBB.
/// Default: white (0xFFFFFF). Call set_background() once at startup.
static BG_RGB: AtomicU32 = AtomicU32::new(0x00FFFFFF);

/// Terminal character cell height in pixels. 0 = unknown (fall back to 6-row
/// sixel bands only). Call detect_cell_height() once at startup.
static CELL_HEIGHT_PX: AtomicU32 = AtomicU32::new(0);

/// Set the background color for alpha compositing. Call once at startup after
/// detecting the terminal background. Only used when sixel is the active protocol.
pub fn set_background(r: u8, g: u8, b: u8) {
    BG_RGB.store(((r as u32) << 16) | ((g as u32) << 8) | (b as u32), Ordering::Relaxed);
}

fn bg_color() -> (u8, u8, u8) {
    let v = BG_RGB.load(Ordering::Relaxed);
    ((v >> 16) as u8, ((v >> 8) & 0xFF) as u8, (v & 0xFF) as u8)
}

/// Query the terminal character cell height via \x1b[16t and store it.
/// Call once at startup. Silently does nothing on failure.
pub fn detect_cell_height(timeout: Duration) {
    if let Some(h) = query_cell_height(timeout) {
        CELL_HEIGHT_PX.store(h, Ordering::Relaxed);
    }
}

fn query_cell_height(timeout: Duration) -> Option<u32> {
    let reply = crate::termbg::query_terminal(b"\x1b[16t", timeout)?;
    parse_cell_height(&reply)
}

/// Parse a `\x1b[16t` reply (`\x1b[6;<height>;<width>t`) into the cell height in
/// pixels. Tolerates a missing ESC[ prefix and requires a positive height.
fn parse_cell_height(reply: &str) -> Option<u32> {
    let body = reply.strip_prefix("\x1b[6;").or_else(|| reply.strip_prefix("[6;"))?;
    let semi = body.find(';')?;
    body[..semi].parse::<u32>().ok().filter(|&h| h > 0)
}

/// Compute the row count to pad an image height to, so it fills complete
/// character cells and complete sixel bands (bands = 6 rows each).
fn pad_to(height: usize) -> usize {
    let cell = CELL_HEIGHT_PX.load(Ordering::Relaxed) as usize;
    // Pad to multiples of the sixel band. If cell height is known, also pad to
    // multiples of cell height so Windows Terminal fills no extra dark cells.
    let quantum = if cell > 0 { lcm(SIXEL_BAND, cell) } else { SIXEL_BAND };
    let r = height % quantum;
    if r == 0 { height } else { height + quantum - r }
}

fn lcm(a: usize, b: usize) -> usize {
    a / gcd(a, b) * b
}

fn gcd(mut a: usize, mut b: usize) -> usize {
    while b != 0 { (a, b) = (b, a % b); }
    a
}

/// Report whether the terminal supports SIXEL, via a DA1 query: send `\x1b[c`
/// and look for attribute `4` (Sixel) in the `\x1b[?<attrs>c` reply.
pub fn supported() -> bool {
    crate::termbg::query_terminal(b"\x1b[c", DA1_TIMEOUT)
        .as_deref()
        .map(parse_da1)
        .unwrap_or(false)
}

/// Return true if a DA1 reply (e.g. "\x1b[?62;4;6c") advertises Sixel (the
/// `;`-separated attribute list contains `4`). Tolerant of malformed input.
fn parse_da1(reply: &str) -> bool {
    let body = match (reply.find("[?"), reply.find('c')) {
        (Some(start), Some(end)) if start + 2 <= end => &reply[start + 2..end],
        _ => return false,
    };
    body.split(';').any(|attr| attr.trim() == "4")
}

/// Encode PNG bytes as a SIXEL escape sequence at native pixel size.
/// Returns an empty vector if the PNG cannot be decoded or encoded; callers
/// already pass raw LaTeX through on render failure, so a no-op write is the
/// safe degradation here.
///
/// `rows` (cell-based row scaling) is currently ignored: SIXEL has no
/// cell-based scaling, so images render at native pixel size. Row-scaling for
/// sixel is not yet implemented.
pub fn encode(png: &[u8], _rows: u32) -> Vec<u8> {
    let (rgba, width, height) = match decode_rgba(png) {
        Some(t) => t,
        None => return Vec::new(),
    };
    // Composite transparent pixels against the background color. RaTeX may
    // leave partially transparent pixels at glyph edges or in padding rows
    // even with an opaque background; the sixel encoder ignores alpha and
    // would render those as black without this step.
    let (br, bg, bb) = bg_color();
    let mut rgba = rgba;
    for px in rgba.chunks_exact_mut(4) {
        let a = px[3] as u32;
        if a < 255 {
            px[0] = ((px[0] as u32 * a + br as u32 * (255 - a)) / 255) as u8;
            px[1] = ((px[1] as u32 * a + bg as u32 * (255 - a)) / 255) as u8;
            px[2] = ((px[2] as u32 * a + bb as u32 * (255 - a)) / 255) as u8;
            px[3] = 255;
        }
    }

    // Pad height to fill complete sixel bands (6 rows) and complete terminal
    // character cells. Unpadded rows show as black in Windows Terminal.
    let padded_height = pad_to(height);
    if padded_height == height || height == 0 {
        return encode_rgba(&rgba, width, height);
    }
    let last_row = rgba[(height - 1) * width * 4..height * width * 4].to_vec();
    for _ in height..padded_height {
        rgba.extend_from_slice(&last_row);
    }
    encode_rgba(&rgba, width, padded_height)
}

/// Encode opaque RGBA8 pixels as a SIXEL escape sequence. Alpha is ignored
/// (our glyph images are opaque). Colors are collected directly with no
/// quantization, since math glyphs use only a handful of distinct shades.
fn encode_rgba(rgba: &[u8], width: usize, height: usize) -> Vec<u8> {
    if width == 0 || height == 0 {
        return Vec::new();
    }

    let mut palette: Vec<(u8, u8, u8)> = Vec::new();
    let mut lookup: HashMap<(u8, u8, u8), usize> = HashMap::new();
    // Per-pixel palette index, row-major.
    let mut indices: Vec<usize> = Vec::with_capacity(width * height);
    // Bits dropped per channel to keep the palette within 256 registers.
    let mut shift: u8 = 0;

    'collect: loop {
        palette.clear();
        lookup.clear();
        indices.clear();
        let mask: u8 = 0xFFu8 << shift;
        for px in rgba.chunks_exact(4) {
            let rgb = (px[0] & mask, px[1] & mask, px[2] & mask);
            let idx = *lookup.entry(rgb).or_insert_with(|| {
                palette.push(rgb);
                palette.len() - 1
            });
            indices.push(idx);
            if palette.len() > 256 {
                shift += 1;
                continue 'collect;
            }
        }
        break;
    }

    let mut out: Vec<u8> = Vec::new();
    // DCS introducer + params + raster attributes (1:1 square pixels).
    out.extend_from_slice(b"\x1bP0;0;0q");
    out.extend_from_slice(format!("\"1;1;{width};{height}").as_bytes());

    // Color registers, RGB on a 0..=COLOR_SCALE scale (rounded).
    for (i, &(r, g, b)) in palette.iter().enumerate() {
        let scale = |c: u8| (c as u32 * COLOR_SCALE + 127) / 255;
        out.extend_from_slice(
            format!("#{i};2;{};{};{}", scale(r), scale(g), scale(b)).as_bytes(),
        );
    }

    let bands = height.div_ceil(SIXEL_BAND);
    for band in 0..bands {
        let mut colors_in_band: Vec<usize> = Vec::new();
        let mut seen = vec![false; palette.len()];
        for r in 0..SIXEL_BAND {
            let y = band * SIXEL_BAND + r;
            if y >= height {
                break;
            }
            for x in 0..width {
                let c = indices[y * width + x];
                if !seen[c] {
                    seen[c] = true;
                    colors_in_band.push(c);
                }
            }
        }

        for (ci, &c) in colors_in_band.iter().enumerate() {
            out.extend_from_slice(format!("#{c}").as_bytes());
            // Build this color's run for the band, then RLE-encode it.
            let mut prev: u8 = 0;
            let mut run: usize = 0;
            for x in 0..width {
                let mut value: u8 = 0;
                for r in 0..SIXEL_BAND {
                    let y = band * SIXEL_BAND + r;
                    if y >= height {
                        break;
                    }
                    if indices[y * width + x] == c {
                        value |= 1 << r;
                    }
                }
                let ch = SIXEL_CHAR_BASE + value;
                if x == 0 {
                    prev = ch;
                    run = 1;
                } else if ch == prev {
                    run += 1;
                } else {
                    flush_run(&mut out, prev, run);
                    prev = ch;
                    run = 1;
                }
            }
            flush_run(&mut out, prev, run);
            if ci + 1 < colors_in_band.len() {
                out.push(b'$'); // graphics CR: reset x for next color
            }
        }
        if band + 1 < bands {
            out.push(b'-'); // graphics newline
        }
    }

    out.extend_from_slice(b"\x1b\\"); // ST
    out
}

/// Emit a run of `n` copies of sixel char `ch`, run-length-encoding runs of 4+.
fn flush_run(out: &mut Vec<u8>, ch: u8, n: usize) {
    if n >= 4 {
        out.extend_from_slice(format!("!{n}").as_bytes());
        out.push(ch);
    } else {
        for _ in 0..n {
            out.push(ch);
        }
    }
}

/// Decode a PNG to 8-bit RGBA, normalizing any input color type/depth.
fn decode_rgba(png: &[u8]) -> Option<(Vec<u8>, usize, usize)> {
    let mut decoder = png::Decoder::new(png);
    decoder.set_transformations(
        png::Transformations::EXPAND | png::Transformations::ALPHA | png::Transformations::STRIP_16,
    );
    let mut reader = decoder.read_info().ok()?;
    let mut buf = vec![0u8; reader.output_buffer_size()];
    let info = reader.next_frame(&mut buf).ok()?;
    if info.color_type != png::ColorType::Rgba || info.bit_depth != png::BitDepth::Eight {
        return None;
    }
    buf.truncate(info.buffer_size());
    Some((buf, info.width as usize, info.height as usize))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn da1_with_sixel() {
        assert!(parse_da1("\x1b[?62;4;6c"));
    }

    #[test]
    fn da1_without_sixel() {
        assert!(!parse_da1("\x1b[?62;6c"));
    }

    #[test]
    fn da1_sixel_only() {
        assert!(parse_da1("\x1b[?4c"));
    }

    #[test]
    fn da1_malformed() {
        assert!(!parse_da1(""));
        assert!(!parse_da1("\x1b[?62;6"));
        assert!(!parse_da1("garbage"));
        // 4 must be a whole attribute, not a substring (e.g. 64).
        assert!(!parse_da1("\x1b[?64;14c"));
    }

    /// Build a tiny opaque RGBA PNG and round-trip it through the SIXEL encoder.
    fn tiny_png() -> Vec<u8> {
        let mut out = Vec::new();
        let mut encoder = png::Encoder::new(&mut out, 2, 2);
        encoder.set_color(png::ColorType::Rgba);
        encoder.set_depth(png::BitDepth::Eight);
        let mut writer = encoder.write_header().unwrap();
        let pixels: [u8; 16] = [
            255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 0, 255,
        ];
        writer.write_image_data(&pixels).unwrap();
        writer.finish().unwrap();
        out
    }

    #[test]
    fn encode_smoke() {
        let png = tiny_png();
        let sixel = encode(&png, 1);
        assert!(sixel.starts_with(b"\x1bP"), "sixel must start with DCS introducer");
        assert!(sixel.ends_with(b"\x1b\\"), "sixel must end with ST");
    }

    #[test]
    fn encode_rgba_structure() {
        // 2x2, two distinct colors: red, green, green, red.
        let pixels: [u8; 16] = [
            255, 0, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 255, 0, 0, 255,
        ];
        let sixel = encode_rgba(&pixels, 2, 2);
        assert!(sixel.starts_with(b"\x1bP"), "must start with DCS introducer");
        assert!(sixel.ends_with(b"\x1b\\"), "must end with ST");
        assert!(contains(&sixel, b"\"1;1;2;2"), "must carry 1:1 raster attrs");
        assert!(contains(&sixel, b"#0;2;"), "must define color register 0");
    }

    fn contains(haystack: &[u8], needle: &[u8]) -> bool {
        haystack.windows(needle.len()).any(|w| w == needle)
    }

    #[test]
    fn gcd_lcm_arithmetic() {
        assert_eq!(gcd(6, 14), 2);
        assert_eq!(gcd(6, 6), 6);
        assert_eq!(gcd(6, 0), 6);
        assert_eq!(lcm(6, 14), 42);
        assert_eq!(lcm(6, 12), 12); // 12 is a multiple of 6
        assert_eq!(lcm(SIXEL_BAND, 1), 6);
    }

    #[test]
    fn pad_to_rounds_up_to_the_band() {
        // With cell height unknown (the default), the quantum is the sixel band
        // (6). These cases avoid mutating the shared CELL_HEIGHT_PX atomic so
        // they stay independent of any concurrently-running encode test.
        assert_eq!(CELL_HEIGHT_PX.load(Ordering::Relaxed), 0, "test assumes default cell height");
        assert_eq!(pad_to(12), 12); // exact multiple of 6
        assert_eq!(pad_to(13), 18); // one over → next band
        assert_eq!(pad_to(7), 12); // one over the first band
        assert_eq!(pad_to(0), 0); // empty guard: 0 % 6 == 0 → unchanged
    }

    #[test]
    fn encode_empty_height_is_guarded() {
        // height == 0 must not panic (no last-row slice) and yields no pixels.
        assert!(encode_rgba(&[], 4, 0).is_empty());
        assert!(encode_rgba(&[], 0, 4).is_empty());
    }

    #[test]
    fn parse_cell_height_reply() {
        // \x1b[16t reply: \x1b[6;<height>;<width>t
        assert_eq!(parse_cell_height("\x1b[6;34;15t"), Some(34));
        // Some terminals drop the ESC; the bare form is accepted too.
        assert_eq!(parse_cell_height("[6;20;10t"), Some(20));
        // Zero height is rejected (treated as unknown).
        assert_eq!(parse_cell_height("\x1b[6;0;10t"), None);
        // Malformed / wrong-report replies yield None.
        assert_eq!(parse_cell_height(""), None);
        assert_eq!(parse_cell_height("\x1b[6;abc;10t"), None);
        assert_eq!(parse_cell_height("\x1b[8;24;80t"), None); // not a cell-size report
        assert_eq!(parse_cell_height("\x1b[6;34"), None); // missing second ';'
    }
}
