// Package unicode renders LaTeX expressions as Unicode text approximations.
//
// It converts LaTeX commands to Unicode equivalents using lookup tables,
// handles super/subscript notation, and passes unknown commands through
// unchanged. This is a best-effort renderer — perfect LaTeX fidelity in
// Unicode is not possible.
//
// This package is stdlib-only and must not import go-latex, go-sixel,
// or the parent render package.
package unicode

import (
	"context"
	"strings"

	"github.com/benn-herrera/laterm/internal/render"
)

// Renderer converts LaTeX expressions to Unicode text.
// It is safe for concurrent use (all state is in the lookup tables,
// which are read-only after init).
type Renderer struct{}

// New returns a Unicode renderer.
func New() *Renderer {
	return &Renderer{}
}

// Render converts a LaTeX expression to a Unicode text approximation.
// The mathType and maxWidth parameters are accepted to satisfy the
// render.Renderer interface but do not affect the output — Unicode text
// is not width-constrained by this renderer (the terminal handles wrapping).
func (r *Renderer) Render(ctx context.Context, expr string, mathType render.MathType, maxWidth int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := renderExpr(expr)
	return []byte(result), nil
}

// renderExpr is the core transformation. It walks the expression left-to-right,
// consuming tokens and emitting Unicode.
func renderExpr(expr string) string {
	var b strings.Builder
	b.Grow(len(expr))

	i := 0
	n := len(expr)

	for i < n {
		switch {
		case expr[i] == '\\':
			i = handleBackslash(expr, i, n, &b)

		case expr[i] == '^':
			i = handleScript(expr, i, n, &b, superscripts, '^')

		case expr[i] == '_':
			i = handleScript(expr, i, n, &b, subscripts, '_')

		case expr[i] == '{' || expr[i] == '}':
			// Bare braces are LaTeX grouping — strip them.
			i++

		default:
			b.WriteByte(expr[i])
			i++
		}
	}

	return b.String()
}

// handleBackslash processes a backslash sequence starting at position i.
// Returns the new position after the consumed token.
func handleBackslash(expr string, i, n int, b *strings.Builder) int {
	// A backslash at the very end — emit it literally.
	if i+1 >= n {
		b.WriteByte('\\')
		return i + 1
	}

	next := expr[i+1]

	// Escaped braces: \{ and \}
	if next == '{' || next == '}' {
		b.WriteByte(next)
		return i + 2
	}

	// Non-letter after backslash (e.g. \, \; \! \\) — emit the pair literally
	// unless it's a recognized spacing command handled above.
	if !isASCIILetter(next) {
		b.WriteByte('\\')
		b.WriteByte(next)
		return i + 2
	}

	// Extract command name: run of ASCII letters.
	start := i + 1
	j := start
	for j < n && isASCIILetter(expr[j]) {
		j++
	}
	cmd := expr[start:j]

	// Strip commands: \left, \right, etc. — consume and emit nothing.
	if stripCommands[cmd] {
		return j
	}

	// Accent commands: \hat{x} → x + combining char.
	if combining, ok := accentCommands[cmd]; ok {
		return handleAccent(expr, j, n, b, combining)
	}

	// \frac{a}{b} → a⁄b
	if cmd == "frac" {
		return handleFrac(expr, j, n, b)
	}

	// \sqrt{x} → √x
	if cmd == "sqrt" {
		return handleSqrt(expr, j, n, b)
	}

	// Regular command lookup.
	if repl, ok := commands[cmd]; ok {
		b.WriteString(repl)
		return j
	}

	// Unknown command — pass through unchanged.
	b.WriteByte('\\')
	b.WriteString(cmd)
	return j
}

// handleAccent processes an accent command whose combining character is known.
// If followed by {content}, renders each content character with the combining
// mark appended. If followed by a single character, applies the accent to that.
// If followed by nothing useful, just emits the combining character.
func handleAccent(expr string, pos, n int, b *strings.Builder, combining string) int {
	content, end := extractArg(expr, pos, n)
	if content == "" {
		// No argument — emit just the combining character (not very useful,
		// but safe).
		b.WriteString(combining)
		return end
	}
	// Apply the combining character after each rune in the content.
	for _, r := range content {
		b.WriteRune(r)
		b.WriteString(combining)
	}
	return end
}

// handleFrac processes \frac{num}{den} → num⁄den.
func handleFrac(expr string, pos, n int, b *strings.Builder) int {
	num, afterNum := extractArg(expr, pos, n)
	den, afterDen := extractArg(expr, afterNum, n)
	b.WriteString(renderExpr(num))
	b.WriteRune('\u2044') // fraction slash
	b.WriteString(renderExpr(den))
	return afterDen
}

// handleSqrt processes \sqrt{expr} → √(expr).
func handleSqrt(expr string, pos, n int, b *strings.Builder) int {
	arg, end := extractArg(expr, pos, n)
	b.WriteRune('√')
	b.WriteString(renderExpr(arg))
	return end
}

// handleScript processes ^ or _ followed by a single char or {group}.
// It tries to convert each character to its Unicode super/subscript form.
// If any character has no mapping, it falls back to prefix notation.
func handleScript(expr string, i, n int, b *strings.Builder, table map[rune]rune, prefix byte) int {
	// Skip the ^ or _ character.
	i++
	if i >= n {
		b.WriteByte(prefix)
		return i
	}

	var content string
	var end int

	if expr[i] == '{' {
		content, end = extractBraced(expr, i, n)
	} else {
		// Single character after ^ or _.
		content = string(expr[i])
		end = i + 1
	}

	// First, recursively render any LaTeX commands within the content.
	rendered := renderExpr(content)

	// Try to convert every rune.
	var converted strings.Builder
	allOK := true
	for _, r := range rendered {
		if mapped, ok := table[r]; ok {
			converted.WriteRune(mapped)
		} else {
			allOK = false
			break
		}
	}

	if allOK {
		b.WriteString(converted.String())
	} else {
		// Fallback: prefix notation.
		b.WriteByte(prefix)
		if len(rendered) > 1 {
			b.WriteByte('{')
			b.WriteString(rendered)
			b.WriteByte('}')
		} else {
			b.WriteString(rendered)
		}
	}

	return end
}

// extractArg extracts the next argument at position pos.
// If pos points to '{', it extracts the brace-delimited content.
// Otherwise it takes a single non-space character (or command) as the argument.
// Returns the argument content and the position after the argument.
func extractArg(expr string, pos, n int) (string, int) {
	// Skip whitespace.
	i := pos
	for i < n && expr[i] == ' ' {
		i++
	}
	if i >= n {
		return "", i
	}
	if expr[i] == '{' {
		return extractBraced(expr, i, n)
	}
	// Single token: if it's a backslash-command, take the whole command.
	if expr[i] == '\\' && i+1 < n && isASCIILetter(expr[i+1]) {
		j := i + 1
		for j < n && isASCIILetter(expr[j]) {
			j++
		}
		return expr[i:j], j
	}
	// Single character.
	return string(expr[i]), i + 1
}

// extractBraced extracts content between matching braces.
// pos must point to the opening '{'.
// Returns the content (without braces) and the position after the closing '}'.
func extractBraced(expr string, pos, n int) (string, int) {
	if pos >= n || expr[pos] != '{' {
		return "", pos
	}
	depth := 1
	start := pos + 1
	i := start
	for i < n && depth > 0 {
		switch expr[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth > 0 {
			i++
		}
	}
	if depth != 0 {
		// Unmatched brace — return everything after the opening brace.
		return expr[start:], n
	}
	return expr[start:i], i + 1
}

func isASCIILetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
