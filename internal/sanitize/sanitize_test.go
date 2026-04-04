package sanitize

import (
	"strings"
	"testing"
)

func TestCheck_AllowedExpressions(t *testing.T) {
	t.Parallel()
	s := New(Config{})

	tests := []struct {
		name string
		expr string
	}{
		{name: "plain text no commands", expr: "x + y = z"},
		{name: "superscript and subscript", expr: "2^{10} + a_i"},
		{name: "greek letters", expr: `\alpha + \beta = \gamma`},
		{name: "uppercase greek", expr: `\Gamma \Delta \Omega`},
		{name: "frac", expr: `\frac{\alpha}{\beta}`},
		{name: "sqrt", expr: `\sqrt{x^2 + y^2}`},
		{name: "sum with limits", expr: `\sum_{i=0}^{n} x_i`},
		{name: "integral", expr: `\int_0^1 f(x) dx`},
		{name: "trig functions", expr: `\sin(\theta) + \cos(\theta)`},
		{name: "relations", expr: `a \leq b \geq c \neq d`},
		{name: "arrows", expr: `A \rightarrow B \Rightarrow C`},
		{name: "delimiters", expr: `\left( \frac{a}{b} \right)`},
		{name: "accents", expr: `\hat{x} + \bar{y} + \vec{z}`},
		{name: "font commands", expr: `\mathbb{R} \mathcal{L} \mathrm{d}x`},
		{name: "text command", expr: `\text{for all } x`},
		{name: "spacing", expr: `a \quad b \qquad c`},
		{name: "misc symbols", expr: `\infty \partial \nabla`},
		{name: "dots", expr: `a_1, \ldots, a_n`},
		{name: "environments", expr: `\begin{matrix} a & b \\ c & d \end{matrix}`},
		{name: "cases environment", expr: `\begin{cases} x & y \\ z & w \end{cases}`},
		{name: "structural", expr: `\binom{n}{k} \overset{?}{=}`},
		{name: "binary ops", expr: `a \pm b \times c \cdot d`},
		{name: "color and cancel", expr: `\color{red} \cancel{x}`},
		{name: "non-letter backslash escapes", expr: `\, \; \! \\ \{ \}`},
		{name: "empty expression", expr: ""},
		{name: "operatorname", expr: `\operatorname{tr}(A)`},
		{name: "boldsymbol", expr: `\boldsymbol{\theta}`},
		{name: "all environments", expr: `\begin{pmatrix} 1 \end{pmatrix} \begin{bmatrix} 2 \end{bmatrix} \begin{Bmatrix} 3 \end{Bmatrix} \begin{vmatrix} 4 \end{vmatrix} \begin{Vmatrix} 5 \end{Vmatrix} \begin{aligned} 6 \end{aligned} \begin{gathered} 7 \end{gathered} \begin{array} 8 \end{array} \begin{subarray} 9 \end{subarray} \begin{split} 10 \end{split}`},
		{name: "backslash at end", expr: `foo\`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := s.Check(tt.expr); err != nil {
				t.Errorf("Check(%q) = %v, want nil", tt.expr, err)
			}
		})
	}
}

func TestCheck_DisallowedCommands(t *testing.T) {
	t.Parallel()
	s := New(Config{})

	tests := []struct {
		name    string
		expr    string
		wantSub string // substring expected in the error message
	}{
		{name: "input", expr: `\input{file}`, wantSub: `\input`},
		{name: "write", expr: `\write{stuff}`, wantSub: `\write`},
		{name: "catcode", expr: `\catcode`, wantSub: `\catcode`},
		{name: "def", expr: `\def\foo{bar}`, wantSub: `\def`},
		{name: "newcommand", expr: `\newcommand{\foo}{bar}`, wantSub: `\newcommand`},
		{name: "include", expr: `\include{chapter}`, wantSub: `\include`},
		{name: "usepackage", expr: `\usepackage{amsmath}`, wantSub: `\usepackage`},
		{name: "eval", expr: `\eval{code}`, wantSub: `\eval`},
		{name: "csname", expr: `\csname foo\endcsname`, wantSub: `\csname`},
		{
			name:    "allowed then disallowed",
			expr:    `\frac{\alpha}{\input{x}}`,
			wantSub: `\input`,
		},
		{
			name:    "disallowed environment",
			expr:    `\begin{document} text \end{document}`,
			wantSub: `\begin{document}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := s.Check(tt.expr)
			if err == nil {
				t.Fatalf("Check(%q) = nil, want error containing %q", tt.expr, tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Check(%q) error = %q, want substring %q", tt.expr, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestCheck_NestingDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		maxDepth int
		expr     string
		wantErr  bool
	}{
		{
			name:     "within default limit",
			maxDepth: 0, // use default
			expr:     strings.Repeat("{", 20) + strings.Repeat("}", 20),
			wantErr:  false,
		},
		{
			name:     "exceeds default limit",
			maxDepth: 0,
			expr:     strings.Repeat("{", 21) + strings.Repeat("}", 21),
			wantErr:  true,
		},
		{
			name:     "custom limit within",
			maxDepth: 5,
			expr:     strings.Repeat("{", 5) + strings.Repeat("}", 5),
			wantErr:  false,
		},
		{
			name:     "custom limit exceeded",
			maxDepth: 5,
			expr:     strings.Repeat("{", 6) + strings.Repeat("}", 6),
			wantErr:  true,
		},
		{
			name:     "nesting resets after close",
			maxDepth: 3,
			expr:     "{{{}}}{{{}}}", // peak depth 3 twice, never 4
			wantErr:  false,
		},
		{
			name:     "no braces",
			maxDepth: 1,
			expr:     "x + y",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := New(Config{MaxNestingDepth: tt.maxDepth})
			err := s.Check(tt.expr)
			if tt.wantErr && err == nil {
				t.Errorf("Check() = nil, want nesting error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Check() = %v, want nil", err)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "nesting depth") {
				t.Errorf("Check() error = %q, want error about nesting depth", err.Error())
			}
		})
	}
}

func TestCheck_EnvironmentEdgeCases(t *testing.T) {
	t.Parallel()
	s := New(Config{})

	tests := []struct {
		name    string
		expr    string
		wantErr bool
		wantSub string
	}{
		{
			name:    "begin without brace",
			expr:    `\begin matrix`,
			wantErr: true,
			wantSub: "missing environment name",
		},
		{
			name:    "begin with unclosed brace",
			expr:    `\begin{matrix`,
			wantErr: true,
			wantSub: "unclosed environment name",
		},
		{
			name:    "end without brace",
			expr:    `\end`,
			wantErr: true,
			wantSub: "missing environment name",
		},
		{
			name:    "begin with whitespace before brace",
			expr:    `\begin {matrix} x \end{matrix}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := s.Check(tt.expr)
			if tt.wantErr && err == nil {
				t.Fatalf("Check(%q) = nil, want error", tt.expr)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Check(%q) = %v, want nil", tt.expr, err)
			}
			if tt.wantErr && err != nil && tt.wantSub != "" && !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Check(%q) error = %q, want substring %q", tt.expr, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestCheck_NonLetterBackslashSequences(t *testing.T) {
	t.Parallel()
	s := New(Config{})

	// Backslash followed by non-letters should not cause rejection.
	exprs := []string{
		`\,`, `\;`, `\!`, `\\`, `\{`, `\}`, `\ `,
		`a \, b`, `x^{2} \\ y^{3}`,
	}
	for _, expr := range exprs {
		if err := s.Check(expr); err != nil {
			t.Errorf("Check(%q) = %v, want nil", expr, err)
		}
	}
}

func FuzzCheck(f *testing.F) {
	f.Add(`\frac{\alpha}{\beta}`)
	f.Add(`\input{/etc/passwd}`)
	f.Add(`\begin{matrix} a \end{matrix}`)
	f.Add(strings.Repeat("{", 100))
	f.Add(``)
	f.Add(`\`)
	f.Add(`\\\\`)
	f.Add(`\begin`)
	f.Add(`\begin{`)
	f.Add(`\end{}`)
	f.Add(`$plain text$`)
	f.Add(`\left(\frac{a}{b}\right)`)

	s := New(Config{})
	f.Fuzz(func(t *testing.T, expr string) {
		// Must not panic.
		_ = s.Check(expr)
	})
}
