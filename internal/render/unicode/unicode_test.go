package unicode

import (
	"context"
	"testing"

	"github.com/ave-veritas-et-enodatio/laterm/internal/render"
)

func TestRender_GreekLetters(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "alpha", expr: `\alpha`, want: "α"},
		{name: "beta", expr: `\beta`, want: "β"},
		{name: "gamma", expr: `\gamma`, want: "γ"},
		{name: "omega", expr: `\omega`, want: "ω"},
		{name: "uppercase Gamma", expr: `\Gamma`, want: "Γ"},
		{name: "uppercase Omega", expr: `\Omega`, want: "Ω"},
		{name: "multiple greek", expr: `\alpha + \beta = \gamma`, want: "α + β = γ"},
		{name: "varepsilon", expr: `\varepsilon`, want: "ɛ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Operators(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "integral", expr: `\int`, want: "∫"},
		{name: "sum", expr: `\sum`, want: "∑"},
		{name: "product", expr: `\prod`, want: "∏"},
		{name: "infinity", expr: `\infty`, want: "∞"},
		{name: "plus-minus", expr: `\pm`, want: "±"},
		{name: "times", expr: `\times`, want: "×"},
		{name: "div", expr: `\div`, want: "÷"},
		{name: "partial", expr: `\partial`, want: "∂"},
		{name: "nabla", expr: `\nabla`, want: "∇"},
		{name: "cdot", expr: `\cdot`, want: "·"},
		{name: "named operator lim", expr: `\lim`, want: "lim"},
		{name: "named operator sin", expr: `\sin`, want: "sin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Superscripts(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "single digit", expr: `x^2`, want: "x²"},
		{name: "braced digit", expr: `x^{2}`, want: "x²"},
		{name: "braced multi-digit", expr: `x^{10}`, want: "x¹⁰"},
		{name: "letter n", expr: `x^n`, want: "xⁿ"},
		{name: "letter i", expr: `a^i`, want: "aⁱ"},
		{name: "plus sign", expr: `x^{n+1}`, want: "xⁿ⁺¹"},
		{name: "minus sign", expr: `x^{-1}`, want: "x⁻¹"},
		{name: "no unicode form fallback", expr: `x^{q}`, want: "x^q"},
		{name: "multi char no form", expr: `x^{qz}`, want: "x^(qz)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Subscripts(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "single digit", expr: `a_0`, want: "a₀"},
		{name: "braced digit", expr: `a_{0}`, want: "a₀"},
		{name: "letter i", expr: `x_i`, want: "xᵢ"},
		{name: "letter n", expr: `a_n`, want: "aₙ"},
		{name: "multi-digit", expr: `a_{12}`, want: "a₁₂"},
		{name: "no unicode form", expr: `a_b`, want: "a_b"},
		{name: "plus sign", expr: `x_{n+1}`, want: "xₙ₊₁"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Frac(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "simple", expr: `\frac{a}{b}`, want: "a⁄b"},
		{name: "with greek", expr: `\frac{\alpha}{\beta}`, want: "α⁄β"},
		{name: "numeric", expr: `\frac{1}{2}`, want: "1⁄2"},
		{name: "nested", expr: `\frac{a+b}{c-d}`, want: "a+b⁄c-d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Sqrt(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "simple", expr: `\sqrt{x}`, want: "√x"},
		{name: "expression", expr: `\sqrt{x^2 + y^2}`, want: "√x² + y²"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Accents(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "hat", expr: `\hat{x}`, want: "x\u0302"},
		{name: "bar", expr: `\bar{y}`, want: "y\u0304"},
		{name: "vec", expr: `\vec{v}`, want: "v\u20D7"},
		{name: "tilde", expr: `\tilde{n}`, want: "n\u0303"},
		{name: "dot", expr: `\dot{x}`, want: "x\u0307"},
		{name: "ddot", expr: `\ddot{x}`, want: "x\u0308"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_UnknownMacros(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "unknown command", expr: `\obscure`, want: `\obscure`},
		{name: "unknown mixed with known", expr: `\alpha + \obscure`, want: `α + \obscure`},
		{name: "unknown command with braces", expr: `\foo{bar}`, want: `\foobar`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Delimiters(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "strip left right", expr: `\left( \frac{a}{b} \right)`, want: "( a⁄b )"},
		{name: "escaped braces", expr: `\{a, b\}`, want: "{a, b}"},
		{name: "angle brackets", expr: `\langle x \rangle`, want: "⟨ x ⟩"},
		{name: "floor", expr: `\lfloor x \rfloor`, want: "⌊ x ⌋"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Relations(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "leq", expr: `a \leq b`, want: "a ≤ b"},
		{name: "neq", expr: `x \neq y`, want: "x ≠ y"},
		{name: "in", expr: `x \in S`, want: "x ∈ S"},
		{name: "forall exists", expr: `\forall x \exists y`, want: "∀ x ∃ y"},
		{name: "subset", expr: `A \subset B`, want: "A ⊂ B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Arrows(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "rightarrow", expr: `A \rightarrow B`, want: "A → B"},
		{name: "Rightarrow", expr: `A \Rightarrow B`, want: "A ⇒ B"},
		{name: "mapsto", expr: `x \mapsto f(x)`, want: "x ↦ f(x)"},
		{name: "implies", expr: `P \implies Q`, want: "P ⟹ Q"},
		{name: "iff", expr: `P \iff Q`, want: "P ⟺ Q"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Spacing(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		// Note: the space after the command name in the input is preserved
		// (this renderer does simple substitution, not TeX-level tokenization).
		{name: "quad", expr: `a\quad b`, want: "a     b"},
		{name: "qquad", expr: `a\qquad b`, want: "a         b"},
		{name: "thinspace", expr: `a\thinspace b`, want: "a\u2009 b"},
		// Without trailing space in input:
		{name: "quad no trailing space", expr: `a\quad{}b`, want: "a    b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_Misc(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "hbar", expr: `\hbar`, want: "ℏ"},
		{name: "ell", expr: `\ell`, want: "ℓ"},
		{name: "emptyset", expr: `\emptyset`, want: "∅"},
		{name: "aleph", expr: `\aleph`, want: "ℵ"},
		{name: "cdots", expr: `a, \cdots, z`, want: "a, ⋯, z"},
		{name: "ldots", expr: `a, \ldots, z`, want: "a, …, z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_ComplexExpressions(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "sum with limits",
			expr: `\sum_{i=0}^{n} x_i`,
			want: "∑ᵢ₌₀ⁿ xᵢ",
		},
		{
			name: "integral",
			expr: `\int_0^1 f(x) dx`,
			want: "∫₀¹ f(x) dx",
		},
		{
			name: "euler identity",
			// π has no Unicode superscript form, so the whole exponent
			// falls back to prefix notation.
			expr: `e^{i\pi} + 1 = 0`,
			want: "e^(iπ) + 1 = 0",
		},
		{
			name: "quadratic formula",
			expr: `\frac{-b \pm \sqrt{b^2 - 4ac}}{2a}`,
			want: "-b ± √b² - 4ac⁄2a",
		},
		{
			name: "empty expression",
			expr: "",
			want: "",
		},
		{
			name: "plain text",
			expr: "x + y = z",
			want: "x + y = z",
		},
		{
			name: "backslash at end",
			expr: `foo\`,
			want: `foo\`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_CancelledContext(t *testing.T) {
	t.Parallel()
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Render(ctx, `\alpha`, render.Inline, 80)
	if err == nil {
		t.Fatal("Render() with cancelled context should return error")
	}
}

// Verify that Renderer satisfies render.Renderer at compile time.
var _ render.Renderer = (*Renderer)(nil)

func TestRender_BinaryOps(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "oplus", expr: `A \oplus B`, want: "A ⊕ B"},
		{name: "otimes", expr: `A \otimes B`, want: "A ⊗ B"},
		{name: "cap", expr: `A \cap B`, want: "A ∩ B"},
		{name: "cup", expr: `A \cup B`, want: "A ∪ B"},
		{name: "setminus", expr: `A \setminus B`, want: "A ∖ B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRender_EdgeCases(t *testing.T) {
	t.Parallel()
	r := New()
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "caret at end", expr: `x^`, want: "x^"},
		{name: "underscore at end", expr: `x_`, want: "x_"},
		{name: "empty braces", expr: `{}`, want: ""},
		{name: "nested braces", expr: `{a{b}c}`, want: "abc"},
		{name: "unmatched open brace in frac", expr: `\frac{a`, want: "a⁄"},
		{name: "double superscript", expr: `x^2^3`, want: "x²³"},
		{name: "non-letter backslash seq", expr: `a \, b`, want: `a \, b`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Render(ctx, tt.expr, render.Inline, 80)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	f.Add(`\alpha + \beta`)
	f.Add(`\frac{1}{2}`)
	f.Add(`x^{2} + y_{i}`)
	f.Add(`\sqrt{x^2 + y^2}`)
	f.Add(`\hat{x} \vec{v}`)
	f.Add(`\obscure`)
	f.Add(`\left( \right)`)
	f.Add(`\{a, b\}`)
	f.Add(``)
	f.Add(`\`)
	f.Add(`^`)
	f.Add(`_`)
	f.Add(`{{{`)
	f.Add(`}}}`)
	f.Add(`\frac`)
	f.Add(`\frac{`)
	f.Add(`\frac{}{}`)
	f.Add(`^{^{^{^{^{x}}}}}`)

	r := New()
	ctx := context.Background()

	f.Fuzz(func(t *testing.T, expr string) {
		// Must not panic.
		_, err := r.Render(ctx, expr, render.Inline, 80)
		if err != nil {
			t.Fatalf("Render() returned unexpected error: %v", err)
		}
	})
}
