// render: convert a LaTeX expression to PNG bytes via RaTeX.

use std::sync::RwLock;

use ratex_layout::{layout, to_display_list, LayoutOptions};
use ratex_parser::parser::parse;
use ratex_render::{render_to_png, RenderOptions};
use ratex_types::{color::Color, math_style::MathStyle};

/// Process-global theme: glyph color and background color.
/// Default: black glyphs on opaque white (fallback when background detection fails).
static THEME: RwLock<Theme> = RwLock::new(Theme {
    glyph: Color::BLACK,
    background: Color::WHITE,
});

struct Theme {
    glyph: Color,
    background: Color,
}

/// Set the global render theme. Call once at startup after detecting the
/// terminal background. `background` alpha 0.0 = transparent.
pub fn set_theme(glyph: Color, background: Color) {
    let mut t = THEME.write().unwrap();
    t.glyph = glyph;
    t.background = background;
}

fn current_theme() -> (Color, Color) {
    let t = THEME.read().unwrap();
    (t.glyph, t.background)
}

/// Errors returned by `render`.
#[derive(Debug)]
pub enum RenderError {
    Parse(String),
    Render(String),
    PngHeader,
    ImageTooLarge,
}

impl std::fmt::Display for RenderError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            RenderError::Parse(e) => write!(f, "parse: {e}"),
            RenderError::Render(e) => write!(f, "render: {e}"),
            RenderError::PngHeader => write!(f, "png header read failed"),
            RenderError::ImageTooLarge => write!(f, "rendered image exceeds size limits"),
        }
    }
}

const MAX_WIDTH: u32 = 4096;
const MAX_HEIGHT: u32 = 4096;

/// Render a LaTeX expression to PNG bytes.
/// Returns (png_bytes, height_in_pixels).
pub fn render(latex: &str, display: bool) -> Result<(Vec<u8>, u32), RenderError> {
    let ast = parse(latex).map_err(|e| RenderError::Parse(format!("{e:?}")))?;

    let (glyph, background) = current_theme();

    let style = if display { MathStyle::Display } else { MathStyle::Text };
    let opts = LayoutOptions::default().with_style(style).with_color(glyph);
    let lbox = layout(&ast, &opts);
    let dl = to_display_list(&lbox);

    let render_opts = RenderOptions {
        background_color: background,
        ..RenderOptions::default()
    };

    let png = render_to_png(&dl, &render_opts).map_err(RenderError::Render)?;

    let (_width, height) = png_dimensions(&png)?;
    Ok((png, height))
}

/// Parse (width, height) in pixels from a PNG's IHDR: width at byte 16, height
/// at byte 20 (big-endian u32). Returns `PngHeader` when the buffer is too short
/// to hold an IHDR, and `ImageTooLarge` when either dimension exceeds the caps.
fn png_dimensions(png: &[u8]) -> Result<(u32, u32), RenderError> {
    if png.len() < 24 {
        return Err(RenderError::PngHeader);
    }
    let width = u32::from_be_bytes([png[16], png[17], png[18], png[19]]);
    let height = u32::from_be_bytes([png[20], png[21], png[22], png[23]]);
    if width > MAX_WIDTH || height > MAX_HEIGHT {
        return Err(RenderError::ImageTooLarge);
    }
    Ok((width, height))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Build a 24-byte buffer whose IHDR width/height fields carry `w`/`h`.
    fn header(w: u32, h: u32) -> Vec<u8> {
        let mut buf = vec![0u8; 24];
        buf[16..20].copy_from_slice(&w.to_be_bytes());
        buf[20..24].copy_from_slice(&h.to_be_bytes());
        buf
    }

    #[test]
    fn short_buffer_is_png_header_error() {
        for len in [0, 1, 16, 23] {
            assert!(matches!(
                png_dimensions(&vec![0u8; len]),
                Err(RenderError::PngHeader)
            ));
        }
    }

    #[test]
    fn valid_header_parses_dimensions() {
        assert_eq!(png_dimensions(&header(640, 480)).unwrap(), (640, 480));
    }

    #[test]
    fn oversized_dimensions_rejected() {
        assert!(matches!(
            png_dimensions(&header(MAX_WIDTH + 1, 10)),
            Err(RenderError::ImageTooLarge)
        ));
        assert!(matches!(
            png_dimensions(&header(10, MAX_HEIGHT + 1)),
            Err(RenderError::ImageTooLarge)
        ));
        // Exactly at the cap is allowed.
        assert_eq!(
            png_dimensions(&header(MAX_WIDTH, MAX_HEIGHT)).unwrap(),
            (MAX_WIDTH, MAX_HEIGHT)
        );
    }
}
