// Package sanitize validates LaTeX expressions against an allowlist of
// known-safe commands before they reach a renderer. It enforces a nesting
// depth budget and rejects any expression containing a disallowed command.
//
// The sanitizer is stdlib-only and does not import any rendering package.
package sanitize

import (
	"fmt"
	"strings"
)

const defaultMaxNestingDepth = 20

// Config controls sanitizer behavior.
type Config struct {
	// MaxNestingDepth is the maximum brace nesting depth permitted.
	// Zero means use the default (20).
	MaxNestingDepth int
}

// Sanitizer validates LaTeX expressions against a static allowlist.
type Sanitizer struct {
	maxDepth int
}

// New creates a Sanitizer with the given configuration.
func New(cfg Config) *Sanitizer {
	depth := cfg.MaxNestingDepth
	if depth <= 0 {
		depth = defaultMaxNestingDepth
	}
	return &Sanitizer{maxDepth: depth}
}

// Check validates expr and returns nil if every command is on the allowlist
// and the nesting depth is within budget. On rejection it returns an error
// describing the first violation found.
func (s *Sanitizer) Check(expr string) error {
	if err := s.checkNesting(expr); err != nil {
		return err
	}
	return s.checkCommands(expr)
}

// checkNesting walks the expression tracking brace depth.
func (s *Sanitizer) checkNesting(expr string) error {
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '{':
			depth++
			if depth > s.maxDepth {
				return fmt.Errorf("nesting depth %d exceeds maximum %d", depth, s.maxDepth)
			}
		case '}':
			depth--
			// Negative depth is a mismatched brace — not our problem to
			// diagnose, but we clamp to zero so it doesn't mask a later
			// legitimate depth violation.
			if depth < 0 {
				depth = 0
			}
		}
	}
	return nil
}

// checkCommands scans for backslash-letter sequences and validates each
// against the allowlist. \begin{env} and \end{env} are handled specially:
// the command itself ("begin"/"end") is always allowed, and the environment
// name is checked against allowedEnvironments.
func (s *Sanitizer) checkCommands(expr string) error {
	i := 0
	n := len(expr)
	for i < n {
		if expr[i] != '\\' {
			i++
			continue
		}
		// Found a backslash. Extract the command name: a run of ASCII letters.
		start := i + 1
		j := start
		for j < n && isASCIILetter(expr[j]) {
			j++
		}
		if j == start {
			// Backslash followed by a non-letter (e.g. \, \; \! \\ \{ \}).
			// These are not letter-commands — skip past the backslash and
			// the single character that follows it.
			i = start + 1
			if i > n {
				i = n
			}
			continue
		}
		cmd := expr[start:j]
		i = j

		if cmd == "begin" || cmd == "end" {
			if err := s.checkEnvironment(expr, cmd, i); err != nil {
				return err
			}
			continue
		}

		if !allowedCommands[cmd] {
			return fmt.Errorf("disallowed command: \\%s", cmd)
		}
	}
	return nil
}

// checkEnvironment extracts the environment name from \begin{name} or
// \end{name} starting at pos (the position just after "begin" or "end")
// and checks it against the allowed environments list.
func (s *Sanitizer) checkEnvironment(expr, cmd string, pos int) error {
	// Skip optional whitespace between the command and the opening brace.
	i := pos
	n := len(expr)
	for i < n && (expr[i] == ' ' || expr[i] == '\t') {
		i++
	}

	if i >= n || expr[i] != '{' {
		// \begin or \end without a brace-delimited argument.
		// Treat the bare command as disallowed — it's not a valid use.
		return fmt.Errorf("disallowed command: \\%s (missing environment name)", cmd)
	}

	// Find the closing brace.
	open := i
	close := strings.IndexByte(expr[open:], '}')
	if close < 0 {
		return fmt.Errorf("disallowed command: \\%s (unclosed environment name)", cmd)
	}
	envName := expr[open+1 : open+close]

	if !allowedEnvironments[envName] {
		return fmt.Errorf("disallowed environment: \\%s{%s}", cmd, envName)
	}
	return nil
}

func isASCIILetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
