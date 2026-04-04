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
		{name: "dfrac", expr: `\dfrac{1}{2}`},
		{name: "tfrac", expr: `\tfrac{a}{b}`},
		{name: "sqrt", expr: `\sqrt{x^2 + y^2}`},
		{name: "sum with limits", expr: `\sum_{i=0}^{n} x_i`},
		{name: "integral", expr: `\int_0^1 f(x) dx`},
		{name: "trig functions", expr: `\sin(\theta) + \cos(\theta)`},
		{name: "additional functions", expr: `\coth x + \lg y + \Pr(A)`},
		{name: "relations", expr: `a \leq b \geq c \neq d`},
		{name: "arrows", expr: `A \rightarrow B \Rightarrow C`},
		{name: "to arrow", expr: `f \to g`},
		{name: "delimiters", expr: `\left( \frac{a}{b} \right)`},
		{name: "accents", expr: `\hat{x} + \bar{y} + \vec{z}`},
		{name: "new accents", expr: `\breve{a} \acute{e} \grave{u} \check{c}`},
		{name: "wide accents", expr: `\widehat{AB} \widetilde{CD}`},
		{name: "font commands", expr: `\mathbb{R} \mathcal{L} \mathrm{d}x`},
		{name: "extended math fonts", expr: `\mathscr{L} \mathdefault{x} \mathregular{y}`},
		{name: "text command", expr: `\text{for all } x`},
		{name: "text font commands", expr: `\textbf{bold} \textit{italic} \textsf{sans}`},
		{name: "extended text fonts", expr: `\textcal{c} \textdefault{d} \textbb{b} \textfrak{f} \textscr{s} \textregular{r} \texttt{t}`},
		{name: "short font names", expr: `\rm x \bf y \it z \tt w`},
		{name: "style switches", expr: `\displaystyle \frac{a}{b}`},
		{name: "spacing", expr: `a \quad b \qquad c`},
		{name: "misc symbols", expr: `\infty \partial \nabla`},
		{name: "more symbols", expr: `\hbar \ell \Re \Im \wp \aleph \beth`},
		{name: "dots", expr: `a_1, \ldots, a_n`},
		{name: "structural", expr: `\binom{n}{k} \stackrel{?}{=}`},
		{name: "binary ops", expr: `a \pm b \times c \cdot d`},
		{name: "color", expr: `\color{red} x`},
		{name: "non-letter backslash escapes", expr: `\, \; \! \\ \{ \}`},
		{name: "empty expression", expr: ""},
		{name: "operatorname", expr: `\operatorname{tr}(A)`},
		{name: "boldsymbol", expr: `\boldsymbol{\theta}`},
		{name: "backslash at end", expr: `foo\`},
		{name: "phantom", expr: `\phantom{x}`},
		{name: "over under braces", expr: `\underbrace{a+b} \overbrace{c+d}`},
		{name: "negation", expr: `\not\equiv`},
		{name: "overline", expr: `\overline{AB}`},
		{name: "overleftarrow", expr: `\overleftarrow{AB}`},
		{name: "tex2unicode integrals", expr: `\iint \iiint`},
		{name: "quantifiers", expr: `\forall x \exists y \nexists z`},
		{name: "notin", expr: `x \notin S`},
		{name: "card symbols", expr: `\clubsuit \diamondsuit \heartsuit \spadesuit`},
		{name: "music symbols", expr: `\flat \natural \sharp`},
		{name: "hspace", expr: `\hspace{1em}`},
		{name: "variant greek", expr: `\varepsilon \vartheta \varpi \varrho \varsigma \varphi`},
		{name: "emptyset variants", expr: `\emptyset \varnothing`},
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
		// \begin/\end are disallowed because the parser panics on them.
		{
			name:    "begin disallowed",
			expr:    `\begin{matrix} a \end{matrix}`,
			wantSub: `\begin`,
		},
		{
			name:    "end disallowed",
			expr:    `\end{cases}`,
			wantSub: `\end`,
		},
		// Commands removed from allowlist: no parser/handler support.
		{name: "implies removed", expr: `A \implies B`, wantSub: `\implies`},
		{name: "iff removed", expr: `A \iff B`, wantSub: `\iff`},
		{name: "cancel removed", expr: `\cancel{x}`, wantSub: `\cancel`},
		{name: "boxed removed", expr: `\boxed{E=mc^2}`, wantSub: `\boxed`},
		{name: "big removed", expr: `\big(`, wantSub: `\big`},
		{name: "underline removed", expr: `\underline{x}`, wantSub: `\underline`},
		{name: "overset removed", expr: `\overset{a}{b}`, wantSub: `\overset`},
		{name: "underset removed", expr: `\underset{a}{b}`, wantSub: `\underset`},
		{name: "smash removed", expr: `\smash{x}`, wantSub: `\smash`},
		{name: "pmod removed", expr: `\pmod{n}`, wantSub: `\pmod`},
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

func TestCheck_ControlCharacters(t *testing.T) {
	t.Parallel()
	s := New(Config{})

	tests := []struct {
		name    string
		expr    string
		wantErr bool
		wantSub string
	}{
		{
			name:    "ESC byte rejected",
			expr:    "x + \x1b[31m red",
			wantErr: true,
			wantSub: "0x1b",
		},
		{
			name:    "null byte rejected",
			expr:    "x\x00y",
			wantErr: true,
			wantSub: "0x00",
		},
		{
			name:    "SOH rejected",
			expr:    "\x01hello",
			wantErr: true,
			wantSub: "0x01",
		},
		{
			name:    "tab allowed",
			expr:    "x\t+ y",
			wantErr: false,
		},
		{
			name:    "newline allowed",
			expr:    "x\n+ y",
			wantErr: false,
		},
		{
			name:    "carriage return allowed",
			expr:    "x\r\n+ y",
			wantErr: false,
		},
		{
			name:    "ESC at position 0",
			expr:    "\x1b[0m",
			wantErr: true,
			wantSub: "position 0",
		},
		{
			name:    "control char position reported",
			expr:    "abc\x1bdef",
			wantErr: true,
			wantSub: "position 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := s.Check(tt.expr)
			if tt.wantErr && err == nil {
				t.Fatalf("Check(%q) = nil, want error containing %q", tt.expr, tt.wantSub)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Check(%q) = %v, want nil", tt.expr, err)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Check(%q) error = %q, want substring %q", tt.expr, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestCheck_ExpressionLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		maxLen  int
		exprLen int
		wantErr bool
		wantSub string
	}{
		{
			name:    "exceeds default limit",
			maxLen:  0, // use default (8192)
			exprLen: 8193,
			wantErr: true,
			wantSub: "exceeds maximum",
		},
		{
			name:    "at default limit passes",
			maxLen:  0,
			exprLen: 8192,
			wantErr: false,
		},
		{
			name:    "exceeds custom limit",
			maxLen:  100,
			exprLen: 101,
			wantErr: true,
			wantSub: "exceeds maximum",
		},
		{
			name:    "at custom limit passes",
			maxLen:  100,
			exprLen: 100,
			wantErr: false,
		},
		{
			name:    "well under limit",
			maxLen:  100,
			exprLen: 10,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := New(Config{MaxExprLength: tt.maxLen})
			expr := strings.Repeat("x", tt.exprLen)
			err := s.Check(expr)
			if tt.wantErr && err == nil {
				t.Fatalf("Check(len=%d) = nil, want error", tt.exprLen)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Check(len=%d) = %v, want nil", tt.exprLen, err)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Check(len=%d) error = %q, want substring %q", tt.exprLen, err.Error(), tt.wantSub)
			}
		})
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
	f.Add("x\x1b[31mred")
	f.Add("\x00\x01\x02")

	s := New(Config{})
	f.Fuzz(func(t *testing.T, expr string) {
		// Must not panic.
		_ = s.Check(expr)
	})
}
