// feed: the output/render-orchestration. Turns conversation/paste text into the
// rendered stdout feed — role markers, role-tinted prose, and math images.
//
// This module (and `input`) are the only ones that write to stdout. Cross-thread
// serialization is the caller's responsibility: `main`'s listener loop, `catch_up`,
// and `input`'s reader thread each hold a shared output mutex while calling
// `emit_entry`.

use std::io::Write;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use crate::{graphics, logging, mathscan, render};

/// Per-role styling for an emitted entry, so the feed is easy to scan: user,
/// assistant/agent, and manually-pasted text. Each entry gets a bold ANSI
/// color-coded opening glyph marker, the prose body tinted in the same color
/// (non-bold) so the whole entry reads as one role at a glance / mid-scroll,
/// and a bold closing glyph mirroring the open. The body color is reset by the
/// closing marker (which ends with a reset) so the blank separator after the
/// entry — and the manual `═` rule — stay untinted.
pub(crate) struct EntryStyle {
    /// Bold color + opening glyph + reset + trailing "> " forward-arrow.
    open: &'static str,
    /// Bold color + leading "<" back-arrow + glyph + reset.
    close: &'static str,
    /// Non-bold color set after `open`, carried across the terminal's soft-wraps.
    body: &'static str,
}
pub(crate) const USER_STYLE: EntryStyle = EntryStyle {
    open: "\x1b[1;32m(u)>\x1b[0m ",
    close: "\x1b[1;32m<(u)\x1b[0m",
    body: "\x1b[32m",
}; // green
pub(crate) const ASSISTANT_STYLE: EntryStyle = EntryStyle {
    open: "\x1b[1;36m[a]>\x1b[0m ",
    close: "\x1b[1;36m<[a]\x1b[0m",
    body: "\x1b[36m",
}; // cyan
pub(crate) const PASTE_STYLE: EntryStyle = EntryStyle {
    open: "\x1b[1;35m{p}>\x1b[0m ",
    close: "\x1b[1;35m<{p}\x1b[0m",
    body: "\x1b[35m",
}; // magenta

/// Tracks whether the last thing written to stdout was a manual separator
/// (Enter in the viewing window). Real content (`emit_entry`) clears it; the
/// `input` read loop refuses to stack a second separator and beeps instead.
static LAST_WAS_SEPARATOR: AtomicBool = AtomicBool::new(false);

/// Arm the manual-separator state: the next Enter should beep rather than draw a
/// second rule. Returns the prior value (true if a separator was already the
/// last output). Used by `input` when handling Enter.
pub(crate) fn arm_separator() -> bool {
    LAST_WAS_SEPARATOR.swap(true, Ordering::Relaxed)
}

const DEFAULT_REF_HEIGHT: u32 = 42;

/// Rendered pixel height of a reference capital "X". A math image is scaled to
/// a row count proportional to its height relative to this, so a capital X
/// displays as exactly one text row and everything scales with surrounding text.
fn reference_height() -> u32 {
    match render::render("X", false) {
        Ok((_, h)) if h > 0 => h,
        _ => DEFAULT_REF_HEIGHT,
    }
}

/// Visual scale bump so math reads at the surrounding text's full line height
/// rather than just cap-height: a capital X alone maps to ~1 row, but everything
/// is nudged up so blocks don't read undersized against the prose around them.
const ROW_SCALE: f32 = 1.25;

/// Row (cell) count for an image of `height_px`, relative to the reference X
/// height `ref_px`, scaled by `ROW_SCALE`. Rounds to nearest and clamps to ≥1.
fn rows_for(height_px: u32, ref_px: u32) -> u32 {
    ((height_px as f32 / ref_px as f32 * ROW_SCALE).round() as u32).max(1)
}

/// Multiple of the reference X height at/above which a rendered expression is
/// laid out as a block (its own line) rather than inline in the text flow.
const BLOCK_THRESHOLD_RATIO: f32 = 1.5;

/// Render context shared by every emit: the reference-X-derived sizing and the
/// selected output protocol. Built once in `main` (the reference "X" is rendered
/// exactly once) and threaded through `emit_entry` so the same three values
/// don't have to ride every signature individually.
pub(crate) struct RenderCtx {
    /// Pixel height at/above which an image is laid out as a block (own line).
    block_threshold: u32,
    /// Reference X height in pixels; the divisor for proportional row sizing.
    ref_height: u32,
    /// The selected terminal image protocol.
    proto: Arc<graphics::Protocol>,
}

impl RenderCtx {
    /// Build the context: render the reference "X" once to derive the reference
    /// height and the block threshold (`BLOCK_THRESHOLD_RATIO`×), and capture the
    /// chosen protocol.
    pub(crate) fn new(proto: Arc<graphics::Protocol>) -> RenderCtx {
        let ref_height = reference_height();
        RenderCtx {
            block_threshold: (ref_height as f32 * BLOCK_THRESHOLD_RATIO) as u32,
            ref_height,
            proto,
        }
    }
}

/// Per-role entry style for a conversation entry, by its `role`.
pub(crate) fn role_style(role: &str) -> &'static EntryStyle {
    match role {
        "user" => &USER_STYLE,
        _ => &ASSISTANT_STYLE,
    }
}

/// Emit one conversation/paste entry: the bold opening role marker, the body
/// tinted in the role's non-bold color, each of the entry's texts rendered (math
/// inline if short, on its own line if tall; prose mirrored verbatim), then the
/// bold closing marker (inline at the end of the content, or on its own line
/// when the content left the cursor at column 0 — e.g. an image-final entry),
/// then a single blank-line separator. Returns the number of math segments
/// emitted (render attempts). Emits nothing (no marker) when the entry has no
/// renderable content. Caller holds the shared output mutex.
pub(crate) fn emit_entry(style: &EntryStyle, texts: &[&str], ctx: &RenderCtx) -> usize {
    let scanned: Vec<Vec<mathscan::Segment>> = texts.iter().map(|t| mathscan::scan(t)).collect();
    if scanned.iter().all(Vec::is_empty) {
        return 0;
    }

    // Real content breaks the manual-separator run, so the next Enter draws one.
    LAST_WAS_SEPARATOR.store(false, Ordering::Relaxed);

    // The caller-held output mutex is the cross-thread serializer; a plain
    // stdout handle under it is enough — no inner stdout lock needed.
    let mut out = std::io::stdout();

    let _ = write!(out, "{}{}", style.open, style.body);
    let mut at_line_start = false; // marker just written
    let mut math_count = 0usize;
    for segs in &scanned {
        math_count += emit_segments(&mut out, segs, &mut at_line_start, ctx);
    }

    // Close marker mirrors the open: inline after mid-line content (separated by
    // a space), or on its own fresh line when the content ended at column 0
    // (image-final). The close marker ends with a reset, so the body color does
    // not leak into the blank separator below.
    if at_line_start {
        let _ = writeln!(out, "{}", style.close);
    } else {
        let _ = writeln!(out, " {}", style.close);
    }
    let _ = writeln!(out);
    math_count
}

/// Walk a flat segment stream, writing text verbatim and rendering math in
/// place. `at_line_start` carries cursor state across calls (so a leading marker
/// and successive text blocks share it). Returns the number of math segments
/// emitted. The caller is responsible for any trailing newline.
fn emit_segments(
    out: &mut impl Write,
    segs: &[mathscan::Segment],
    at_line_start: &mut bool,
    ctx: &RenderCtx,
) -> usize {
    let mut math_count = 0usize;

    for seg in segs {
        match seg.kind {
            mathscan::Kind::Text => {
                if !seg.text.is_empty() {
                    let _ = write!(out, "{}", seg.text);
                    *at_line_start = seg.text.ends_with('\n');
                }
            }
            mathscan::Kind::Math => {
                math_count += 1;
                match render::render(&seg.text, seg.display) {
                    Ok((png, height)) if height >= ctx.block_threshold => {
                        let rows = rows_for(height, ctx.ref_height);
                        if !*at_line_start {
                            let _ = writeln!(out);
                        }
                        let _ = out.write_all(&ctx.proto.encode(&png, rows));
                        let _ = writeln!(out);
                        *at_line_start = true;
                    }
                    Ok((png, height)) => {
                        let rows = rows_for(height, ctx.ref_height);
                        let _ = out.write_all(&ctx.proto.encode(&png, rows));
                        *at_line_start = false;
                    }
                    Err(e) => {
                        // Pass raw LaTeX through on failure.
                        logging::warn(&format!("render failed ({e}), passing through raw latex"));
                        let delim = if seg.display { "$$" } else { "$" };
                        let _ = write!(out, "{delim}{}{delim}", seg.text);
                        *at_line_start = false;
                    }
                }
            }
        }
    }

    math_count
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rows_for_rounds_and_clamps() {
        // Scaled by ROW_SCALE (1.25), then rounded to nearest.
        assert_eq!(rows_for(42, 42), 1); // 1.0 * 1.25 = 1.25 -> 1
        assert_eq!(rows_for(126, 42), 4); // 3.0 * 1.25 = 3.75 -> 4
        assert_eq!(rows_for(59, 42), 2); // 1.40 * 1.25 = 1.76 -> 2
        assert_eq!(rows_for(67, 42), 2); // 1.60 * 1.25 = 1.99 -> 2
        // Clamps to at least 1 even for tiny images.
        assert_eq!(rows_for(0, 42), 1);
        assert_eq!(rows_for(5, 42), 1); // 0.12 * 1.25 = 0.15 -> 0 -> clamp 1
    }

    #[test]
    fn role_marker_maps_user_and_assistant() {
        assert_eq!(role_style("user").open, USER_STYLE.open);
        assert_eq!(role_style("assistant").open, ASSISTANT_STYLE.open);
        // Unknown roles fall back to the assistant style.
        assert_eq!(role_style("system").open, ASSISTANT_STYLE.open);
    }
}
