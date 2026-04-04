package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benn-herrera/laterm/internal/render"
	"github.com/benn-herrera/laterm/internal/sanitize"
	"github.com/benn-herrera/laterm/internal/statemachine"
)

// slowReader wraps a reader and adds a delay between reads. This helps test
// timing-dependent behavior without races.
type slowReader struct {
	r     io.Reader
	delay time.Duration
}

func (sr *slowReader) Read(p []byte) (int, error) {
	if sr.delay > 0 {
		time.Sleep(sr.delay)
	}
	return sr.r.Read(p)
}

// fakeRenderer implements render.Renderer for testing.
type fakeRenderer struct {
	mu       sync.Mutex
	result   []byte
	err      error
	delay    time.Duration
	calls    []renderCall
	renderFn func(ctx context.Context, expr string, mathType render.MathType, maxWidth int) ([]byte, error)
}

type renderCall struct {
	Expr     string
	MathType render.MathType
	MaxWidth int
}

func (f *fakeRenderer) Render(ctx context.Context, expr string, mathType render.MathType, maxWidth int) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, renderCall{Expr: expr, MathType: mathType, MaxWidth: maxWidth})
	delay := f.delay
	result := f.result
	err := f.err
	fn := f.renderFn
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fn != nil {
		return fn(ctx, expr, mathType, maxWidth)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// syncWriter is a thread-safe bytes.Buffer.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *syncWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

func TestPlainTextPassthrough(t *testing.T) {
	input := "hello world\n"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := writer.String()
	if got != input {
		t.Errorf("output = %q, want %q", got, input)
	}
}

func TestANSIPassthrough(t *testing.T) {
	// CSI sequence with embedded $ must pass through unmodified.
	input := "\x1b[0$m normal text"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := writer.String()
	if got != input {
		t.Errorf("output = %q, want %q", got, input)
	}
}

func TestShellVariablePassthrough(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"PATH", "$PATH"},
		{"HOME", "$HOME"},
		{"subshell", "$(cmd)"},
		{"expansion", "${var}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			writer := &syncWriter{}

			loop := NewLoop(Config{
				Reader:    reader,
				Writer:    writer,
				Machine:   statemachine.New(statemachine.Config{}),
				Sanitizer: sanitize.New(sanitize.Config{}),
			})

			err := loop.Run(context.Background())
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			got := writer.String()
			if got != tt.input {
				t.Errorf("output = %q, want %q", got, tt.input)
			}
		})
	}
}

func TestInlineMathRender(t *testing.T) {
	input := "$\\sigma$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{result: []byte("[rendered:sigma]")}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Wait briefly for the render goroutine.
	time.Sleep(50 * time.Millisecond)

	got := writer.String()
	if got != "[rendered:sigma]" {
		t.Errorf("output = %q, want %q", got, "[rendered:sigma]")
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.calls) != 1 {
		t.Fatalf("render called %d times, want 1", len(fr.calls))
	}
	if fr.calls[0].Expr != "\\sigma" {
		t.Errorf("render expr = %q, want %q", fr.calls[0].Expr, "\\sigma")
	}
	if fr.calls[0].MathType != render.Inline {
		t.Errorf("render mathType = %v, want Inline", fr.calls[0].MathType)
	}
}

func TestBlockMathRender(t *testing.T) {
	input := "$$\\int_0^1$$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{result: []byte("[rendered:integral]")}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	got := writer.String()
	if got != "[rendered:integral]" {
		t.Errorf("output = %q, want %q", got, "[rendered:integral]")
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.calls) != 1 {
		t.Fatalf("render called %d times, want 1", len(fr.calls))
	}
	if fr.calls[0].MathType != render.Block {
		t.Errorf("render mathType = %v, want Block", fr.calls[0].MathType)
	}
}

func TestRenderFailureFallsBackToLiteral(t *testing.T) {
	input := "$\\alpha$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{err: errors.New("render failed")}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	got := writer.String()
	want := "$\\alpha$"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestSanitizerRejectionFallsBackToLiteral(t *testing.T) {
	// \input is not on the allowlist.
	input := "$\\input{file}$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{result: []byte("should not appear")}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	got := writer.String()
	want := "$\\input{file}$"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.calls) != 0 {
		t.Errorf("render was called %d times, want 0 (sanitizer should have blocked)", len(fr.calls))
	}
}

func TestNoRendererFlushesLiteral(t *testing.T) {
	input := "$\\beta$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		// No Renderer.
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := writer.String()
	want := "$\\beta$"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPostMathBuffering(t *testing.T) {
	// Simulate: math expression followed by text that arrives during render.
	// We need the render to take some time so that text after the math
	// expression gets buffered.
	//
	// Input: "$\\alpha$ trailing"
	// The render takes 100ms; "trailing" arrives in the same read batch
	// after math completes, but the render goroutine is still running, so
	// " trailing" gets buffered and flushed after render completes.
	input := "$\\alpha$ trailing"
	reader := &slowReader{r: strings.NewReader(input)}
	writer := &syncWriter{}

	fr := &fakeRenderer{
		result: []byte("[rendered]"),
		delay:  100 * time.Millisecond,
	}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Wait for the render goroutine to complete and flush.
	time.Sleep(200 * time.Millisecond)

	got := writer.String()
	// The rendered math comes first, then the trailing text.
	want := "[rendered] trailing"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPostMathBufferOverflow(t *testing.T) {
	// Small buffer limit. The render takes a long time, and we send
	// enough data to overflow the post-math buffer.
	input := "$\\alpha$" + strings.Repeat("x", 100)
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{
		result: []byte("[rendered]"),
		delay:  200 * time.Millisecond,
	}

	loop := NewLoop(Config{
		Reader:          reader,
		Writer:          writer,
		Machine:         statemachine.New(statemachine.Config{}),
		Sanitizer:       sanitize.New(sanitize.Config{}),
		Renderer:        fr,
		PostMathBufSize: 10, // very small
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	got := writer.String()
	// On overflow, the render result is discarded. The "x" bytes are written
	// directly. We should see all 100 x's but NOT the rendered result.
	if strings.Contains(got, "[rendered]") {
		t.Error("rendered result should have been discarded on overflow")
	}
	xCount := strings.Count(got, "x")
	if xCount != 100 {
		t.Errorf("got %d x's, want 100", xCount)
	}
}

func TestContextCancellation(t *testing.T) {
	// A reader that blocks forever, cancelled by context.
	pr, pw := io.Pipe()
	defer pw.Close()

	writer := &syncWriter{}

	loop := NewLoop(Config{
		Reader:    pr,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- loop.Run(ctx)
	}()

	// Write some data, then cancel.
	_, _ = pw.Write([]byte("hello"))
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Close the pipe to unblock the Read.
	pw.Close()

	select {
	case err := <-done:
		if err == nil {
			// Acceptable — pipe close caused EOF before context check.
		} else if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestUpdateMaxWidth(t *testing.T) {
	input := "$\\gamma$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	var capturedWidth int
	fr := &fakeRenderer{
		renderFn: func(_ context.Context, _ string, _ render.MathType, maxWidth int) ([]byte, error) {
			capturedWidth = maxWidth
			return []byte("[ok]"), nil
		},
	}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
		MaxWidth:  120,
	})

	// Update width before running.
	loop.UpdateMaxWidth(200)

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if capturedWidth != 200 {
		t.Errorf("renderer received maxWidth = %d, want 200", capturedWidth)
	}
}

func TestTimeBudgetExpiry(t *testing.T) {
	// Use a pipe so we can control timing precisely.
	pr, pw := io.Pipe()

	writer := &syncWriter{}

	loop := NewLoop(Config{
		Reader:        pr,
		Writer:        writer,
		Machine:       statemachine.New(statemachine.Config{}),
		Sanitizer:     sanitize.New(sanitize.Config{}),
		InlineTimeout: 100 * time.Millisecond,
	})

	done := make(chan error, 1)
	go func() {
		done <- loop.Run(context.Background())
	}()

	// Send the start of an inline math expression but don't close it.
	_, _ = pw.Write([]byte("$\\alpha"))
	// Wait for the time budget to expire.
	time.Sleep(250 * time.Millisecond)
	// Send more data to trigger the drainTimeBudget check.
	_, _ = pw.Write([]byte(" more"))
	time.Sleep(50 * time.Millisecond)
	pw.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}

	got := writer.String()
	// The incomplete math expression should be flushed as literal.
	if !strings.Contains(got, "$\\alpha") {
		t.Errorf("output = %q, should contain the flushed literal \"$\\alpha\"", got)
	}
	if !strings.Contains(got, " more") {
		t.Errorf("output = %q, should contain \" more\"", got)
	}
}

func TestConsecutiveMathExpressions(t *testing.T) {
	input := "$a$ and $b$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	callNum := 0
	fr := &fakeRenderer{
		renderFn: func(_ context.Context, expr string, _ render.MathType, _ int) ([]byte, error) {
			callNum++
			return []byte("[" + expr + "]"), nil
		},
	}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	got := writer.String()
	want := "[a] and [b]"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestBlockMathFallbackLiteral(t *testing.T) {
	input := "$$\\int_0^1 f(x) dx$$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{err: errors.New("render failed")}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	got := writer.String()
	want := "$$\\int_0^1 f(x) dx$$"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRawExpressionReconstruction(t *testing.T) {
	tests := []struct {
		name    string
		content string
		isBlock bool
		want    string
	}{
		{"inline", "x+y", false, "$x+y$"},
		{"block", "x+y", true, "$$x+y$$"},
		{"empty inline", "", false, "$$"},
		{"empty block", "", true, "$$$$"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(rawExpression(tt.content, tt.isBlock))
			if got != tt.want {
				t.Errorf("rawExpression(%q, %v) = %q, want %q", tt.content, tt.isBlock, got, tt.want)
			}
		})
	}
}

func TestMixedContentOrdering(t *testing.T) {
	// Verify that text before math, rendered math, and text after math
	// all appear in the correct order.
	input := "before $\\alpha$ after"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{result: []byte("[MATH]")}

	loop := NewLoop(Config{
		Reader:    reader,
		Writer:    writer,
		Machine:   statemachine.New(statemachine.Config{}),
		Sanitizer: sanitize.New(sanitize.Config{}),
		Renderer:  fr,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	got := writer.String()
	want := "before [MATH] after"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRenderTimeout(t *testing.T) {
	input := "$\\alpha$"
	reader := strings.NewReader(input)
	writer := &syncWriter{}

	fr := &fakeRenderer{
		delay: 5 * time.Second, // longer than the render timeout
		renderFn: func(ctx context.Context, _ string, _ render.MathType, _ int) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	loop := NewLoop(Config{
		Reader:        reader,
		Writer:        writer,
		Machine:       statemachine.New(statemachine.Config{}),
		Sanitizer:     sanitize.New(sanitize.Config{}),
		Renderer:      fr,
		RenderTimeout: 50 * time.Millisecond,
	})

	err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	got := writer.String()
	// Should fall back to literal since render timed out.
	want := "$\\alpha$"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
