// mathscan: find LaTeX math expressions in a markdown string and group them
// into render units with surrounding prose context.
//
// Recognised delimiters:
//   Display (display=true):  $$..$$  and  \[..\]
//   Inline  (display=false): $..$    and  \(..\)
//
// \$ is a literal dollar. \\ followed by $ is an escaped backslash then a
// real $. Unterminated spans and empty expressions are treated as literal text.

/// Maximum runes of surrounding prose kept as before/after context.
const ANCHOR_BUDGET: usize = 40;

/// Kind of a segment: literal text or math expression.
#[derive(Debug, Clone, PartialEq)]
pub enum Kind {
    Text,
    Math,
}

/// One element of a unit: literal text or a math expression.
/// For Math, `text` holds the inner expression (delimiters stripped, trimmed)
/// and `display` reports block vs inline.
#[derive(Debug, Clone, PartialEq)]
pub struct Segment {
    pub kind: Kind,
    pub text: String,
    pub display: bool,
}

/// One math-bearing logical line. `segments` holds that line's text and math
/// interleaved in document order. `before` and `after` are short windows of
/// the nearest prose from adjacent non-math lines.
#[derive(Debug, Clone, PartialEq)]
pub struct Unit {
    pub before: String,
    pub segments: Vec<Segment>,
    pub after: String,
}

fn txt(s: &str) -> Segment {
    Segment { kind: Kind::Text, text: s.to_string(), display: false }
}

fn math(s: &str, display: bool) -> Segment {
    Segment { kind: Kind::Math, text: s.to_string(), display }
}

/// Scan returns one Unit per math-bearing line of text, in document order.
/// Returns None / empty when text contains no math.
pub fn scan(text: &str) -> Vec<Unit> {
    let toks = tokenize(text);
    if toks.is_empty() {
        return vec![];
    }
    assemble(&toks)
}

// ---- tokenizer ----

#[derive(Debug, Clone)]
struct Token {
    is_math: bool,
    text: String,
    display: bool,
}

/// Tokenize splits text into literal text tokens and math tokens.
/// Returns empty if no math is found.
fn tokenize(text: &str) -> Vec<Token> {
    let runes: Vec<char> = text.chars().collect();
    let n = runes.len();
    let mut toks: Vec<Token> = Vec::new();
    let mut buf: Vec<char> = Vec::new();
    let mut saw_math = false;

    let flush = |buf: &mut Vec<char>, toks: &mut Vec<Token>| {
        if !buf.is_empty() {
            toks.push(Token { is_math: false, text: buf.iter().collect(), display: false });
            buf.clear();
        }
    };

    let mut i = 0;
    while i < n {
        let r = runes[i];

        // \\ -> literal escaped backslash; next rune is not special.
        if r == '\\' && i + 1 < n && runes[i + 1] == '\\' {
            buf.push('\\');
            buf.push('\\');
            i += 2;
            continue;
        }
        // \$ -> literal dollar.
        if r == '\\' && i + 1 < n && runes[i + 1] == '$' {
            buf.push('\\');
            buf.push('$');
            i += 2;
            continue;
        }
        // \( -> inline math;  \[ -> display math.
        if r == '\\' && i + 1 < n && (runes[i + 1] == '(' || runes[i + 1] == '[') {
            let display = runes[i + 1] == '[';
            let close: Vec<char> = if display { vec!['\\', ']'] } else { vec!['\\', ')'] };
            if let Some((expr, end)) = scan_to(&runes, i + 2, &close) {
                flush(&mut buf, &mut toks);
                toks.push(Token { is_math: true, text: expr, display });
                saw_math = true;
                i = end;
                continue;
            }
            buf.push(r);
            buf.push(runes[i + 1]);
            i += 2;
            continue;
        }
        // $$ -> display math (must be tested before single $).
        if r == '$' && i + 1 < n && runes[i + 1] == '$' {
            if let Some((expr, end)) = scan_to(&runes, i + 2, &['$', '$']) {
                flush(&mut buf, &mut toks);
                toks.push(Token { is_math: true, text: expr, display: true });
                saw_math = true;
                i = end;
                continue;
            }
            buf.push('$');
            buf.push('$');
            i += 2;
            continue;
        }
        // $ -> inline math.
        if r == '$' {
            if let Some((expr, end)) = scan_to(&runes, i + 1, &['$']) {
                flush(&mut buf, &mut toks);
                toks.push(Token { is_math: true, text: expr, display: false });
                saw_math = true;
                i = end;
                continue;
            }
            buf.push('$');
            i += 1;
            continue;
        }

        buf.push(r);
        i += 1;
    }
    flush(&mut buf, &mut toks);

    if !saw_math {
        return vec![];
    }
    toks
}

/// Scan from `start` looking for `close_seq`, honoring \\ and \$ escapes.
/// Returns (trimmed inner expression, index after close) or None if unterminated
/// or empty.
fn scan_to(runes: &[char], start: usize, close_seq: &[char]) -> Option<(String, usize)> {
    let n = runes.len();
    let mut pos = start;
    while pos < n {
        if runes[pos] == '\\' && pos + 1 < n && (runes[pos + 1] == '\\' || runes[pos + 1] == '$') {
            pos += 2;
            continue;
        }
        if match_at(runes, pos, close_seq) {
            let inner: String = runes[start..pos].iter().collect();
            let inner = inner.trim().to_string();
            if inner.is_empty() {
                return None;
            }
            return Some((inner, pos + close_seq.len()));
        }
        pos += 1;
    }
    None
}

fn match_at(runes: &[char], pos: usize, want: &[char]) -> bool {
    if pos + want.len() > runes.len() {
        return false;
    }
    runes[pos..pos + want.len()] == *want
}

// ---- assembler ----

struct Line {
    segs: Vec<Segment>,
    has_math: bool,
    text: String, // plain text content for neighbor context
}

fn assemble(toks: &[Token]) -> Vec<Unit> {
    let mut lines: Vec<Line> = Vec::new();
    let mut cur = Line { segs: Vec::new(), has_math: false, text: String::new() };

    let push_line = |lines: &mut Vec<Line>, cur: &mut Line| {
        let next = Line { segs: Vec::new(), has_math: false, text: String::new() };
        lines.push(std::mem::replace(cur, next));
    };

    for t in toks {
        if t.is_math {
            cur.segs.push(math(&t.text, t.display));
            cur.has_math = true;
            continue;
        }
        // Split text tokens on newlines to reconstruct source line structure.
        let parts: Vec<&str> = t.text.split('\n').collect();
        for (i, p) in parts.iter().enumerate() {
            if i > 0 {
                push_line(&mut lines, &mut cur);
            }
            if !p.is_empty() {
                cur.segs.push(txt(p));
                cur.text.push_str(p);
            }
        }
    }
    push_line(&mut lines, &mut cur);

    // The last math-bearing line is the only one that gets trailing (After)
    // context. Prose between two formulas is claimed by the following formula's
    // Before, not duplicated.
    let last_math = lines.iter().enumerate().rev().find(|(_, l)| l.has_math).map(|(i, _)| i);

    let mut units: Vec<Unit> = Vec::new();
    for (i, ln) in lines.iter().enumerate() {
        if !ln.has_math {
            continue;
        }
        let mut segs: Vec<Segment> = ln.segs.clone();
        bound_same_line(&mut segs);

        let before = if segs[0].kind == Kind::Math {
            context_before(&gather_context(&lines, i, -1))
        } else {
            String::new()
        };
        let after = if segs[segs.len() - 1].kind == Kind::Math && Some(i) == last_math {
            context_after(&gather_context(&lines, i, 1))
        } else {
            String::new()
        };

        units.push(Unit { before, segments: segs, after });
    }

    if units.is_empty() {
        return vec![];
    }
    units
}

/// Trim the text segments of a unit to the anchor budget.
fn bound_same_line(segs: &mut [Segment]) {
    let last = segs.len() - 1;
    for (i, seg) in segs.iter_mut().enumerate() {
        if seg.kind != Kind::Text {
            continue;
        }
        let s = seg.text.clone();
        seg.text = if i == 0 {
            trim_tail(&s)
        } else if i == last {
            trim_head(&s)
        } else {
            trim_middle(&s)
        };
    }
}

/// Gather text of consecutive non-math lines adjacent to line `i` in direction `dir`.
/// Stops at the next math line or document boundary. Blank lines are crossed.
fn gather_context(lines: &[Line], i: usize, dir: i32) -> String {
    let mut parts: Vec<&str> = Vec::new();
    let mut j = i as i64 + dir as i64;
    while j >= 0 && j < lines.len() as i64 {
        let idx = j as usize;
        if lines[idx].has_math {
            break;
        }
        if !lines[idx].text.is_empty() {
            parts.push(&lines[idx].text);
        }
        j += dir as i64;
    }
    if dir < 0 {
        parts.reverse();
    }
    parts.join(" ")
}

fn context_before(s: &str) -> String {
    trim_tail(&collapse(s))
}

fn context_after(s: &str) -> String {
    trim_head(&collapse(s))
}

fn trim_tail(s: &str) -> String {
    let (t, truncated) = tail_runes(s, ANCHOR_BUDGET);
    if truncated {
        format!("\u{2026}{t}")
    } else {
        t
    }
}

fn trim_head(s: &str) -> String {
    let (h, truncated) = head_runes(s, ANCHOR_BUDGET);
    if truncated {
        format!("{h}\u{2026}")
    } else {
        h
    }
}

fn trim_middle(s: &str) -> String {
    let runes: Vec<char> = s.chars().collect();
    if runes.len() <= 2 * ANCHOR_BUDGET {
        return s.to_string();
    }
    let (h, _) = head_runes(s, ANCHOR_BUDGET);
    let (t, _) = tail_runes(s, ANCHOR_BUDGET);
    format!("{h}\u{2026}{t}")
}

/// Return up to `n` runes from the start of `s`, snapped back to the last word
/// boundary when truncated. The bool reports whether truncation occurred.
fn head_runes(s: &str, n: usize) -> (String, bool) {
    let r: Vec<char> = s.chars().collect();
    if r.len() <= n {
        return (s.to_string(), false);
    }
    let mut head = &r[..n];
    if let Some(k) = head.iter().rposition(|&c| c == ' ') {
        head = &head[..k];
    }
    (head.iter().collect(), true)
}

/// Return up to `n` runes from the end of `s`, snapped forward to the next word
/// boundary when truncated. The bool reports whether truncation occurred.
fn tail_runes(s: &str, n: usize) -> (String, bool) {
    let r: Vec<char> = s.chars().collect();
    if r.len() <= n {
        return (s.to_string(), false);
    }
    let tail = &r[r.len() - n..];
    let start = if let Some(k) = tail.iter().position(|&c| c == ' ') { k + 1 } else { 0 };
    (tail[start..].iter().collect(), true)
}

/// Replace every run of whitespace (including newlines) with a single space.
fn collapse(s: &str) -> String {
    s.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn t(s: &str) -> Segment { txt(s) }
    fn inl(s: &str) -> Segment { math(s, false) }
    fn dis(s: &str) -> Segment { math(s, true) }
    fn unit(before: &str, after: &str, segs: Vec<Segment>) -> Unit {
        Unit { before: before.to_string(), after: after.to_string(), segments: segs }
    }

    #[test]
    fn no_math() {
        assert!(scan("just some text with no math at all").is_empty());
    }

    #[test]
    fn inline_only() {
        assert_eq!(scan(r"$\sigma$"), vec![unit("", "", vec![inl(r"\sigma")])]);
    }

    #[test]
    fn inline_with_leading_and_trailing_text() {
        assert_eq!(
            scan(r"the value $x^2$ is positive"),
            vec![unit("", "", vec![t("the value "), inl("x^2"), t(" is positive")])]
        );
    }

    #[test]
    fn two_inline_on_one_line_stay_in_one_unit() {
        assert_eq!(
            scan(r"first $\sigma^2$ and then $\sqrt{2}$, done"),
            vec![unit("", "", vec![
                t("first "), inl(r"\sigma^2"), t(" and then "), inl(r"\sqrt{2}"), t(", done"),
            ])]
        );
    }

    #[test]
    fn display_block() {
        assert_eq!(
            scan(r"$$\int_0^1 f(x)\,dx$$"),
            vec![unit("", "", vec![dis(r"\int_0^1 f(x)\,dx")])]
        );
    }

    #[test]
    fn paren_bracket_delimiters() {
        assert_eq!(
            scan(r"inline \(a+b\) and block \[c+d\]"),
            vec![unit("", "", vec![t("inline "), inl("a+b"), t(" and block "), dis("c+d")])]
        );
    }

    #[test]
    fn escaped_dollar_is_literal_no_math() {
        assert!(scan(r"it costs \$5 today").is_empty());
    }

    #[test]
    fn empty_span_is_literal_no_math() {
        assert!(scan("nothing $$ here").is_empty());
    }

    #[test]
    fn unterminated_span_is_literal_no_math() {
        assert!(scan("a lone $ sign").is_empty());
    }

    #[test]
    fn same_line_text_anchors_formula_neighbor_lines_suppressed() {
        assert_eq!(
            scan("intro prose line\nresult: $E=mc^2$ here\ntrailing prose line"),
            vec![unit("", "", vec![t("result: "), inl("E=mc^2"), t(" here")])]
        );
    }

    #[test]
    fn display_block_alone_pulls_context_from_neighbor_lines_across_blanks() {
        let input = "and its integral form:\n\n$$\n\\sum x\n$$\n\nTwo things to check";
        assert_eq!(
            scan(input),
            vec![unit("and its integral form:", "Two things to check", vec![dis(r"\sum x")])]
        );
    }

    #[test]
    fn two_math_lines_each_anchored_by_own_line_text() {
        assert_eq!(
            scan("first $a$ line\nmiddle prose\nsecond $b$ line"),
            vec![
                unit("", "", vec![t("first "), inl("a"), t(" line")]),
                unit("", "", vec![t("second "), inl("b"), t(" line")]),
            ]
        );
    }

    #[test]
    fn neighbor_context_truncated_with_ellipsis_and_word_snapped() {
        assert_eq!(
            scan("the quick brown fox jumps over the lazy dog repeatedly today\n$x$"),
            vec![unit("\u{2026}over the lazy dog repeatedly today", "", vec![inl("x")])]
        );
    }

    #[test]
    fn prose_between_two_display_blocks_is_claimed_once() {
        assert_eq!(
            scan("Block one:\n$$x$$\nMiddle:\n$$y$$\nEnd."),
            vec![
                unit("Block one:", "", vec![dis("x")]),
                unit("Middle:", "End.", vec![dis("y")]),
            ]
        );
    }

    #[test]
    fn long_leading_same_line_text_is_bounded_to_tail_window() {
        assert_eq!(
            scan("this is a very long sentence with plenty of words before the math $x$ ok"),
            vec![unit("", "", vec![
                t("\u{2026}with plenty of words before the math "), inl("x"), t(" ok"),
            ])]
        );
    }
}
