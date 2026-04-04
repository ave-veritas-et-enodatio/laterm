// Copyright ©2024 The go-latex Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mtex

import (
	"testing"

	"github.com/benn-herrera/laterm/internal/golatex/drawtex"
	"github.com/benn-herrera/laterm/internal/golatex/font/ttf"
)

// fixedExpr describes a LaTeX expression that previously caused a panic. The
// stillPanics field marks cases where the underlying go-latex handler is not
// yet implemented — those are skipped and tracked for future work.
type fixedExpr struct {
	name        string
	expr        string
	stillPanics bool // true = known-unhandled, skip until fixed
}

// fixedExprs holds LaTeX expressions that were identified as panic sources in
// the vendored go-latex code. Expressions where stillPanics is false have been
// fixed and must not regress.
var fixedExprs = []fixedExpr{
	// Priority 1: previously known panics.
	{"mathcal", `$\mathcal{M}$`, false},
	{"mathbb", `$\mathbb{R}$`, false},
	{"mathbf", `$\mathbf{x}$`, false},
	{"mathit", `$\mathit{x}$`, false},
	{"left_right_paren_frac", `$\left(\frac{a}{b}\right)$`, false},
	{"left_right_bracket_frac", `$\left[\frac{a}{b}\right]$`, false},
	{"text", `$\text{hello}$`, false},
	{"operatorname", `$\operatorname{argmax}$`, false},
	{"bare_parens", `$(x + y)$`, false},
	{"func_with_parens", `$f(x) = x^2$`, false},
	// Priority 2: additional fixed constructs.
	{"accent_hat", `$\hat{x}$`, false},
	{"accent_vec", `$\vec{v}$`, false},
	{"accent_bar", `$\bar{x}$`, false},
	{"accent_dot", `$\dot{x}$`, false},
	{"punctuation", `$a, b; c$`, false},
	{"mathrm", `$\mathrm{Var}$`, false},
	{"displaystyle_sum", `$\displaystyle \sum_{i=1}^{n}$`, false},
	{"stackrel", `$\stackrel{a}{b}$`, false},
	// Priority 3: combined expressions (integration tests).
	{"loss_function", `$\mathcal{L} = \sum_{i} \left(\hat{y}_i - y_i\right)^2$`, false},
	{"derivative_defn", `$\frac{\partial f}{\partial x} = \lim_{h \to 0} \frac{f(x+h) - f(x)}{h}$`, false},
	{"function_between_spaces", `$\mathbb{R}^n \to \mathbb{R}^m$`, false},
	{"mle", `$\operatorname{argmax}_\theta \sum_{i=1}^{N} \log p(x_i | \theta)$`, true},                                        // unhandled "|" token
}

// TestFixedExpressions_Render verifies that LaTeX expressions which previously
// caused panics now complete the full Render pipeline (parse, layout, ship,
// draw) without panicking.
//
// Expressions where the underlying handler is still unimplemented are skipped
// (stillPanics == true). When a handler is added, flip the flag to false so
// the test enforces the fix.
//
// A returned error is acceptable — some panics were converted to proper errors.
// A panic on a non-skipped test is a failure.
func TestFixedExpressions_Render(t *testing.T) {
	const (
		dpi    = 72
		ftsize = 10
	)

	for _, tt := range fixedExprs {
		t.Run(tt.name, func(t *testing.T) {
			if tt.stillPanics {
				t.Skipf("known-unhandled: %s still panics (handler not yet implemented)", tt.name)
			}
			err := renderNoPanic(t, tt.expr, ftsize, dpi)
			if err != nil {
				t.Logf("Render returned error (not a panic): %v", err)
			}
		})
	}
}

// renderNoPanic calls Render with the dummyRenderer (defined in render_test.go)
// and reports panics as test failures. A returned error is not a failure.
func renderNoPanic(t *testing.T, expr string, ftsize, dpi float64) (rerr error) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic rendering %q: %v", expr, r)
		}
	}()

	return Render(dummyRenderer{}, expr, ftsize, dpi, nil)
}

// TestFixedExpressions_Parse isolates the parse phase from rendering. It
// constructs a real ttf.Backend (backed by a drawtex.Canvas) and calls Parse
// directly. This helps distinguish parse-time panics from render-time panics.
func TestFixedExpressions_Parse(t *testing.T) {
	const (
		dpi    = 72
		ftsize = 10
	)

	canvas := drawtex.New()
	be := ttf.New(canvas)

	for _, tt := range fixedExprs {
		t.Run(tt.name, func(t *testing.T) {
			if tt.stillPanics {
				t.Skipf("known-unhandled: %s still panics (handler not yet implemented)", tt.name)
			}
			err := parseNoPanic(t, tt.expr, ftsize, dpi, be)
			if err != nil {
				t.Logf("Parse returned error (not a panic): %v", err)
			}
		})
	}
}

// parseNoPanic calls Parse with a real ttf backend and reports panics as test
// failures. A returned error is not a failure.
func parseNoPanic(t *testing.T, expr string, ftsize, dpi float64, be *ttf.Backend) (rerr error) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic parsing %q: %v", expr, r)
		}
	}()

	box, err := Parse(expr, ftsize, dpi, be)
	if err != nil {
		return err
	}
	if box == nil {
		t.Fatalf("Parse returned nil box for %q", expr)
	}
	return nil
}

// TestStillPanics_Smoke is a diagnostic test that verifies the "stillPanics"
// annotations are accurate. It runs every expression marked stillPanics==true
// and confirms it does indeed panic. If a marked expression stops panicking
// (because a handler was added), this test fails — signaling that the
// stillPanics flag should be flipped to false.
func TestStillPanics_Smoke(t *testing.T) {
	const (
		dpi    = 72
		ftsize = 10
	)

	for _, tt := range fixedExprs {
		if !tt.stillPanics {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			panicked := didPanic(tt.expr, ftsize, dpi)
			if !panicked {
				t.Errorf("%s: marked stillPanics but did not panic — flip stillPanics to false", tt.name)
			}
		})
	}
}

// didPanic returns true if Render panics for the given expression.
func didPanic(expr string, ftsize, dpi float64) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()

	_ = Render(dummyRenderer{}, expr, ftsize, dpi, nil)
	return false
}
