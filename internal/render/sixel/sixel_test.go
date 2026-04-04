package sixel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/benn-herrera/laterm/internal/render"
)

func TestNew_Defaults(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	if r.maxPixelWidth != defaultMaxPixelWidth {
		t.Errorf("maxPixelWidth = %d, want %d", r.maxPixelWidth, defaultMaxPixelWidth)
	}
	if r.maxPixelHeight != defaultMaxPixelHeight {
		t.Errorf("maxPixelHeight = %d, want %d", r.maxPixelHeight, defaultMaxPixelHeight)
	}
	if r.dpi != defaultDPI {
		t.Errorf("dpi = %f, want %f", r.dpi, defaultDPI)
	}
	if r.logger == nil {
		t.Error("logger is nil")
	}
}

func TestNew_CustomConfig(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	r := New(Config{
		MaxPixelWidth:  1024,
		MaxPixelHeight: 768,
		DPI:            144,
		Logger:         logger,
	})

	if r.maxPixelWidth != 1024 {
		t.Errorf("maxPixelWidth = %d, want 1024", r.maxPixelWidth)
	}
	if r.maxPixelHeight != 768 {
		t.Errorf("maxPixelHeight = %d, want 768", r.maxPixelHeight)
	}
	if r.dpi != 144 {
		t.Errorf("dpi = %f, want 144", r.dpi)
	}
	if r.logger != logger {
		t.Error("logger not set to provided logger")
	}
}

func TestRender_SimpleExpression(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	ctx := context.Background()

	data, err := r.Render(ctx, `f(x)=ax+b`, render.Inline, 0)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Render() returned empty data")
	}

	// Sixel data starts with DCS (ESC P) and ends with ST (ESC \).
	if !bytes.HasPrefix(data, []byte("\033P")) {
		t.Errorf("Sixel data does not start with DCS introducer")
	}
	if !bytes.HasSuffix(data, []byte("\033\\")) {
		t.Errorf("Sixel data does not end with ST")
	}
}

func TestRender_MathExpressions(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	ctx := context.Background()

	tests := []struct {
		name     string
		expr     string
		mathType render.MathType
	}{
		{name: "inline_linear", expr: `f(x)=ax+b`, mathType: render.Inline},
		{name: "inline_sqrt", expr: `\sqrt{x}`, mathType: render.Inline},
		{name: "block_frac", expr: `\frac{\sqrt{x+20}}{2\pi}`, mathType: render.Block},
		{name: "delta_neq", expr: `\delta x \neq \frac{\sqrt{x+20}}{2\Delta}`, mathType: render.Inline},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := r.Render(ctx, tc.expr, tc.mathType, 0)
			if err != nil {
				t.Fatalf("Render(%q) error: %v", tc.expr, err)
			}
			if len(data) == 0 {
				t.Fatalf("Render(%q) returned empty data", tc.expr)
			}
			if !bytes.HasPrefix(data, []byte("\033P")) {
				t.Errorf("Sixel data for %q does not start with DCS", tc.expr)
			}
		})
	}
}

func TestRender_AlreadyDelimited(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	ctx := context.Background()

	// Expression already wrapped in $ delimiters should still work.
	data, err := r.Render(ctx, `$f(x)$`, render.Inline, 0)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Render() returned empty data")
	}
}

func TestRender_CancelledContext(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Render(ctx, `f(x)=ax+b`, render.Inline, 0)
	if err == nil {
		t.Fatal("Render() with cancelled context should return error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestRender_ContextTimeout(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give the context time to expire.
	time.Sleep(1 * time.Millisecond)

	_, err := r.Render(ctx, `f(x)=ax+b`, render.Inline, 0)
	if err == nil {
		t.Fatal("Render() with expired context should return error")
	}
}

func TestRender_ImageTooLarge(t *testing.T) {
	t.Parallel()

	// Use extremely small pixel limits to force the size check to fail.
	r := New(Config{
		MaxPixelWidth:  1,
		MaxPixelHeight: 1,
		DPI:            96,
	})
	ctx := context.Background()

	_, err := r.Render(ctx, `f(x)=ax+b`, render.Inline, 0)
	if err == nil {
		t.Fatal("expected ErrImageTooLarge, got nil")
	}
	if !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("error = %v, want ErrImageTooLarge", err)
	}
}

func TestRender_MaxWidthOverride(t *testing.T) {
	t.Parallel()

	// Set a generous renderer limit but pass a tiny maxWidth to Render.
	r := New(Config{
		MaxPixelWidth:  2000,
		MaxPixelHeight: 2000,
		DPI:            96,
	})
	ctx := context.Background()

	_, err := r.Render(ctx, `f(x)=ax+b`, render.Inline, 1)
	if err == nil {
		t.Fatal("expected ErrImageTooLarge when maxWidth=1, got nil")
	}
	if !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("error = %v, want ErrImageTooLarge", err)
	}
}

func TestWrapExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expr     string
		mathType render.MathType
		want     string
	}{
		{name: "bare_inline", expr: `x+1`, mathType: render.Inline, want: `$x+1$`},
		{name: "bare_block", expr: `x+1`, mathType: render.Block, want: `$x+1$`},
		{name: "already_delimited", expr: `$x+1$`, mathType: render.Inline, want: `$x+1$`},
		{name: "single_char", expr: `x`, mathType: render.Inline, want: `$x$`},
		{name: "empty", expr: ``, mathType: render.Inline, want: `$$`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wrapExpr(tc.expr, tc.mathType)
			if got != tc.want {
				t.Errorf("wrapExpr(%q, %v) = %q, want %q", tc.expr, tc.mathType, got, tc.want)
			}
		})
	}
}

// TestRender_InterfaceCompliance verifies the compile-time interface check
// is present and correct.
func TestRender_InterfaceCompliance(t *testing.T) {
	t.Parallel()

	var r render.Renderer = New(Config{})
	if r == nil {
		t.Fatal("New() returned nil")
	}
}
