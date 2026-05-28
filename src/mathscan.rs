// mathscan: find LaTeX math expressions in a markdown string and return the
// full interleaved text/math stream in document order.
//
// Recognised delimiters:
//   Display (display=true):  $$..$$  and  \[..\]
//   Inline  (display=false): $..$    and  \(..\)
//
// \$ is a literal dollar. \\ followed by $ is an escaped backslash then a
// real $. Unterminated spans and empty expressions are treated as literal text.

/// Kind of a segment: literal text or math expression.
#[derive(Debug, Clone, PartialEq)]
pub enum Kind {
    Text,
    Math,
}

/// One element of the stream: literal text or a math expression.
/// For Math, `text` holds the inner expression (delimiters stripped, trimmed)
/// and `display` reports block vs inline. For Text, `text` is verbatim source
/// (newlines preserved) and `display` is unused.
#[derive(Debug, Clone, PartialEq)]
pub struct Segment {
    pub kind: Kind,
    pub text: String,
    pub display: bool,
}

fn txt(s: &str) -> Segment {
    Segment {
        kind: Kind::Text,
        text: s.to_string(),
        display: false,
    }
}

fn math(s: &str, display: bool) -> Segment {
    Segment {
        kind: Kind::Math,
        text: s.to_string(),
        display,
    }
}

/// Scan returns the full interleaved text/math stream in document order, with
/// newlines preserved inside Text segments. Empty input yields an empty vec;
/// text with no math yields a single Text segment holding the whole text.
pub fn scan(text: &str) -> Vec<Segment> {
    tokenize(text)
        .into_iter()
        .map(|t| {
            if t.is_math {
                math(&t.text, t.display)
            } else {
                txt(&t.text)
            }
        })
        .collect()
}

// ---- tokenizer ----

#[derive(Debug, Clone)]
struct Token {
    is_math: bool,
    text: String,
    display: bool,
}

/// Tokenize splits text into literal text tokens and math tokens, in order.
fn tokenize(text: &str) -> Vec<Token> {
    let runes: Vec<char> = text.chars().collect();
    let n = runes.len();
    let mut toks: Vec<Token> = Vec::new();
    let mut buf: Vec<char> = Vec::new();

    let flush = |buf: &mut Vec<char>, toks: &mut Vec<Token>| {
        if !buf.is_empty() {
            toks.push(Token {
                is_math: false,
                text: buf.iter().collect(),
                display: false,
            });
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
            let close: Vec<char> = if display {
                vec!['\\', ']']
            } else {
                vec!['\\', ')']
            };
            if let Some((expr, end)) = scan_to(&runes, i + 2, &close) {
                flush(&mut buf, &mut toks);
                toks.push(Token {
                    is_math: true,
                    text: expr,
                    display,
                });
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
                toks.push(Token {
                    is_math: true,
                    text: expr,
                    display: true,
                });
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
                toks.push(Token {
                    is_math: true,
                    text: expr,
                    display: false,
                });
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

#[cfg(test)]
mod tests {
    use super::*;

    fn t(s: &str) -> Segment {
        txt(s)
    }
    fn inl(s: &str) -> Segment {
        math(s, false)
    }
    fn dis(s: &str) -> Segment {
        math(s, true)
    }

    #[test]
    fn empty_input_is_empty() {
        assert!(scan("").is_empty());
    }

    #[test]
    fn no_math_returns_whole_text_as_one_segment() {
        let s = "just some text with no math at all";
        assert_eq!(scan(s), vec![t(s)]);
    }

    #[test]
    fn inline_only() {
        assert_eq!(scan(r"$\sigma$"), vec![inl(r"\sigma")]);
    }

    #[test]
    fn inline_with_leading_and_trailing_text() {
        assert_eq!(
            scan(r"the value $x^2$ is positive"),
            vec![t("the value "), inl("x^2"), t(" is positive")]
        );
    }

    #[test]
    fn two_inline_on_one_line() {
        assert_eq!(
            scan(r"first $\sigma^2$ and then $\sqrt{2}$, done"),
            vec![
                t("first "),
                inl(r"\sigma^2"),
                t(" and then "),
                inl(r"\sqrt{2}"),
                t(", done")
            ]
        );
    }

    #[test]
    fn display_block() {
        assert_eq!(
            scan(r"$$\int_0^1 f(x)\,dx$$"),
            vec![dis(r"\int_0^1 f(x)\,dx")]
        );
    }

    #[test]
    fn paren_bracket_delimiters() {
        assert_eq!(
            scan(r"inline \(a+b\) and block \[c+d\]"),
            vec![t("inline "), inl("a+b"), t(" and block "), dis("c+d")]
        );
    }

    #[test]
    fn multiline_preserves_newlines_and_interleaves_math() {
        assert_eq!(
            scan("intro prose line\nresult: $E=mc^2$ here\ntrailing prose line"),
            vec![
                t("intro prose line\nresult: "),
                inl("E=mc^2"),
                t(" here\ntrailing prose line"),
            ]
        );
    }

    #[test]
    fn display_block_across_blank_lines_keeps_surrounding_text() {
        let input = "and its integral form:\n\n$$\n\\sum x\n$$\n\nTwo things to check";
        assert_eq!(
            scan(input),
            vec![
                t("and its integral form:\n\n"),
                dis(r"\sum x"),
                t("\n\nTwo things to check"),
            ]
        );
    }

    #[test]
    fn escaped_dollar_is_literal_text() {
        let s = r"it costs \$5 today";
        assert_eq!(scan(s), vec![t(s)]);
    }

    #[test]
    fn empty_span_is_literal_text() {
        let s = "nothing $$ here";
        assert_eq!(scan(s), vec![t(s)]);
    }

    #[test]
    fn unterminated_span_is_literal_text() {
        let s = "a lone $ sign";
        assert_eq!(scan(s), vec![t(s)]);
    }
}
