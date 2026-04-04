// Package stream implements the single read-loop / single write-path data flow
// between a child PTY and the user's terminal. It feeds bytes from the PTY
// reader into a state machine, dispatches detected math expressions through a
// sanitizer and renderer, and writes all output (passthrough text, rendered
// math, flushed literals) to a single writer. It manages a bounded post-math
// output buffer that accumulates bytes arriving during an in-flight render.
package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/benn-herrera/laterm/internal/render"
	"github.com/benn-herrera/laterm/internal/sanitize"
	"github.com/benn-herrera/laterm/internal/statemachine"
)

const (
	defaultInlineTimeout  = 200 * time.Millisecond
	defaultBlockTimeout   = 10 * time.Second
	defaultRenderTimeout  = 5 * time.Second
	defaultPostMathBufSz  = 32768
	defaultMaxWidth       = 80
	readBufSize           = 4096
)

// Config configures the stream processing loop.
type Config struct {
	Reader    io.Reader            // child PTY output
	Writer    io.Writer            // user's terminal
	Machine   *statemachine.Machine
	Sanitizer *sanitize.Sanitizer
	Renderer  render.Renderer
	Logger    *slog.Logger

	MaxWidth        int           // terminal width in columns for rendering
	InlineTimeout   time.Duration // time budget for inline math (default 200ms)
	BlockTimeout    time.Duration // time budget for block math (default 10s)
	RenderTimeout   time.Duration // wall-clock timeout for rendering (default 5s)
	PostMathBufSize int           // max bytes to buffer during render (default 32768)
}

func (c Config) withDefaults() Config {
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	if c.MaxWidth <= 0 {
		c.MaxWidth = defaultMaxWidth
	}
	if c.InlineTimeout <= 0 {
		c.InlineTimeout = defaultInlineTimeout
	}
	if c.BlockTimeout <= 0 {
		c.BlockTimeout = defaultBlockTimeout
	}
	if c.RenderTimeout <= 0 {
		c.RenderTimeout = defaultRenderTimeout
	}
	if c.PostMathBufSize <= 0 {
		c.PostMathBufSize = defaultPostMathBufSz
	}
	return c
}

// Loop is the main stream processing loop. It reads from a child PTY, feeds
// bytes through a state machine, and writes all output to a single writer.
type Loop struct {
	cfg Config

	mu        sync.Mutex // serializes all writes to cfg.Writer
	rendering bool       // true while a render goroutine is in flight
	overflow  bool       // true if postBuf exceeded its limit during render
	postBuf   []byte     // bytes buffered while a render is in flight

	// renderDone is signaled when the render goroutine finishes. The main
	// loop checks this to clear the rendering flag after flushing postBuf.
	renderDone chan struct{}

	// timeBudget carries the result of a time-budget expiry from the timer
	// goroutine to the main loop. The timer goroutine sends; the main loop
	// receives with a non-blocking select between read batches.
	timeBudget chan struct{}

	// timerMu protects timer start/stop from racing with the timer callback.
	timerMu sync.Mutex
	timer   *time.Timer

	// inMath tracks whether the state machine is buffering math, so the
	// timer is started only on the first BufferForMath per expression.
	inMath bool
}

// NewLoop creates a new stream processing loop with the given configuration.
// The caller must provide non-nil Reader, Writer, Machine, and Sanitizer.
// Renderer may be nil, in which case math expressions are flushed as literal text.
func NewLoop(cfg Config) *Loop {
	cfg = cfg.withDefaults()

	if cfg.Reader == nil {
		cfg.Logger.Error("stream.NewLoop: nil Reader")
	}
	if cfg.Writer == nil {
		cfg.Logger.Error("stream.NewLoop: nil Writer")
	}
	if cfg.Machine == nil {
		cfg.Logger.Error("stream.NewLoop: nil Machine")
	}

	return &Loop{
		cfg:        cfg,
		renderDone: make(chan struct{}, 1),
		timeBudget: make(chan struct{}, 1),
	}
}

// UpdateMaxWidth updates the terminal width used for rendering. It is safe to
// call from any goroutine (e.g., a SIGWINCH handler).
func (l *Loop) UpdateMaxWidth(width int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if width > 0 {
		l.cfg.MaxWidth = width
	}
}

// Run reads from the child PTY and processes bytes until EOF or context
// cancellation. This is the main goroutine — it blocks until the child exits.
func (l *Loop) Run(ctx context.Context) error {
	if l.cfg.Reader == nil || l.cfg.Writer == nil || l.cfg.Machine == nil {
		return errors.New("stream: missing required configuration (Reader, Writer, or Machine)")
	}

	buf := make([]byte, readBufSize)
	for {
		// Check context before each read.
		if err := ctx.Err(); err != nil {
			l.shutdown()
			return err
		}

		// Non-blocking drain of timer expiry between read batches.
		l.drainTimeBudget()

		n, readErr := l.cfg.Reader.Read(buf)
		if n > 0 {
			l.processBatch(buf[:n])
		}

		if readErr != nil {
			l.shutdown()
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("stream: read: %w", readErr)
		}
	}
}

// processBatch feeds a batch of bytes through the state machine one at a time
// and handles each resulting action.
func (l *Loop) processBatch(data []byte) {
	for _, b := range data {
		// Check for timer expiry between each byte for responsiveness.
		l.drainTimeBudget()

		action := l.cfg.Machine.Feed(b)
		l.handleAction(action)
	}
}

// handleAction processes a single action returned by the state machine.
func (l *Loop) handleAction(action statemachine.Action) {
	switch action.Kind {
	case statemachine.ActionNone:
		// Nothing to do.

	case statemachine.ActionEmit:
		l.writeOrBuffer(action.Data)

	case statemachine.ActionBufferForMath:
		if !l.inMath {
			l.inMath = true
			l.startTimeBudget()
		}

	case statemachine.ActionMathComplete:
		l.inMath = false
		l.cancelTimeBudget()
		l.dispatchRender(action.Content, action.IsBlock)

	case statemachine.ActionFlushLiteral:
		l.inMath = false
		l.cancelTimeBudget()
		l.writeOrBuffer(action.Data)
	}
}

// writeOrBuffer writes data to the terminal if no render is in flight, or
// appends it to the post-math buffer if a render is active.
func (l *Loop) writeOrBuffer(data []byte) {
	if len(data) == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.rendering {
		l.writeLocked(data)
		return
	}

	// Rendering in flight — buffer for later.
	if l.overflow {
		// Already overflowed; just write directly (render result will be discarded).
		l.writeLocked(data)
		return
	}

	if len(l.postBuf)+len(data) > l.cfg.PostMathBufSize {
		// Overflow: flush the post buffer directly and mark overflow so the
		// render result is discarded when it arrives.
		l.cfg.Logger.Warn("stream: post-math buffer overflow, flushing",
			slog.Int("buf_len", len(l.postBuf)),
			slog.Int("incoming", len(data)),
			slog.Int("limit", l.cfg.PostMathBufSize),
		)
		l.overflow = true
		if len(l.postBuf) > 0 {
			l.writeLocked(l.postBuf)
			l.postBuf = l.postBuf[:0]
		}
		l.writeLocked(data)
		return
	}

	l.postBuf = append(l.postBuf, data...)
}

// writeLocked writes data to the terminal writer. Must be called with l.mu held.
func (l *Loop) writeLocked(data []byte) {
	_, err := l.cfg.Writer.Write(data)
	if err != nil {
		l.cfg.Logger.Error("stream: write to terminal", slog.String("error", err.Error()))
	}
}

// dispatchRender starts a render goroutine for the given math expression.
// If no renderer is configured, the expression is flushed as literal text.
func (l *Loop) dispatchRender(content string, isBlock bool) {
	if l.cfg.Renderer == nil {
		l.flushRawExpression(content, isBlock)
		return
	}

	// Wait for any previous render to finish before starting a new one.
	// This should be rare in practice; it means two math expressions arrived
	// back-to-back with the first still rendering.
	l.waitForRender()

	l.mu.Lock()
	l.rendering = true
	l.overflow = false
	l.postBuf = l.postBuf[:0]
	l.mu.Unlock()

	mathType := render.Inline
	if isBlock {
		mathType = render.Block
	}

	// Capture maxWidth under lock to avoid racing with UpdateMaxWidth.
	l.mu.Lock()
	maxWidth := l.cfg.MaxWidth
	l.mu.Unlock()

	go l.renderAndWrite(content, isBlock, mathType, maxWidth)
}

// renderAndWrite runs in a goroutine. It sanitizes, renders, and writes the
// result to the terminal, then flushes the post-math buffer.
func (l *Loop) renderAndWrite(content string, isBlock bool, mathType render.MathType, maxWidth int) {
	defer func() {
		l.renderDone <- struct{}{}
	}()

	// 1. Sanitize.
	if l.cfg.Sanitizer != nil {
		if err := l.cfg.Sanitizer.Check(content); err != nil {
			l.cfg.Logger.Info("stream: sanitizer rejected expression",
				slog.String("error", err.Error()),
			)
			l.finishRender(nil, content, isBlock)
			return
		}
	}

	// 2. Render with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), l.cfg.RenderTimeout)
	defer cancel()

	rendered, err := l.cfg.Renderer.Render(ctx, content, mathType, maxWidth)
	if err != nil {
		l.cfg.Logger.Warn("stream: render failed, flushing literal",
			slog.String("error", err.Error()),
		)
		l.finishRender(nil, content, isBlock)
		return
	}

	// 3. Write rendered output and flush post buffer.
	l.finishRender(rendered, content, isBlock)
}

// finishRender writes the render result (or raw fallback) and flushes the
// post-math buffer. If rendered is nil, the raw expression is written as
// literal text instead.
func (l *Loop) finishRender(rendered []byte, content string, isBlock bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.overflow {
		// Post buffer overflowed during render — the raw text was already
		// flushed to the terminal. Discard the render result.
		l.rendering = false
		return
	}

	if rendered != nil {
		l.writeLocked(rendered)
	} else {
		// Fallback: write raw delimiters + content.
		raw := rawExpression(content, isBlock)
		l.writeLocked(raw)
	}

	// Flush buffered post-math output.
	if len(l.postBuf) > 0 {
		l.writeLocked(l.postBuf)
		l.postBuf = l.postBuf[:0]
	}

	l.rendering = false
}

// rawExpression reconstructs the original delimited expression for literal
// passthrough when rendering fails or is unavailable.
func rawExpression(content string, isBlock bool) []byte {
	if isBlock {
		raw := make([]byte, 0, len(content)+4)
		raw = append(raw, '$', '$')
		raw = append(raw, content...)
		raw = append(raw, '$', '$')
		return raw
	}
	raw := make([]byte, 0, len(content)+2)
	raw = append(raw, '$')
	raw = append(raw, content...)
	raw = append(raw, '$')
	return raw
}

// flushRawExpression writes a math expression as literal text (with delimiters)
// when no renderer is available.
func (l *Loop) flushRawExpression(content string, isBlock bool) {
	raw := rawExpression(content, isBlock)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeLocked(raw)
}

// waitForRender blocks until any in-flight render goroutine finishes.
func (l *Loop) waitForRender() {
	l.mu.Lock()
	isRendering := l.rendering
	l.mu.Unlock()

	if isRendering {
		<-l.renderDone
	}
}

// startTimeBudget starts or resets the time-budget timer. When the timer fires,
// it sends on l.timeBudget so the main loop can call TimeBudgetExpired on the
// state machine.
func (l *Loop) startTimeBudget() {
	// Determine timeout based on machine state.
	timeout := l.cfg.InlineTimeout
	state := l.cfg.Machine.State()
	if state == statemachine.StateBlockMath || state == statemachine.StateBlockMathClosing {
		timeout = l.cfg.BlockTimeout
	}

	l.timerMu.Lock()
	defer l.timerMu.Unlock()

	if l.timer != nil {
		l.timer.Stop()
	}

	l.timer = time.AfterFunc(timeout, func() {
		// Non-blocking send — if there's already a pending signal, don't block.
		select {
		case l.timeBudget <- struct{}{}:
		default:
		}
	})
}

// cancelTimeBudget stops the time-budget timer and drains any pending signal.
func (l *Loop) cancelTimeBudget() {
	l.timerMu.Lock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.timerMu.Unlock()

	// Drain any pending signal.
	select {
	case <-l.timeBudget:
	default:
	}
}

// drainTimeBudget checks for a pending time-budget expiry and, if present,
// tells the state machine to flush. Called from the main loop between reads
// and between individual bytes.
func (l *Loop) drainTimeBudget() {
	select {
	case <-l.timeBudget:
		action := l.cfg.Machine.TimeBudgetExpired()
		if action.Kind != statemachine.ActionNone {
			l.inMath = false
			l.handleAction(action)
		}
	default:
	}
}

// shutdown performs cleanup when the main loop exits. It flushes any pending
// state machine buffer as literal text and waits for in-flight renders.
func (l *Loop) shutdown() {
	l.cancelTimeBudget()

	// If the machine is mid-math, flush as literal.
	action := l.cfg.Machine.TimeBudgetExpired()
	if action.Kind != statemachine.ActionNone {
		l.inMath = false
		l.writeOrBuffer(action.Data)
	}

	// Wait for any in-flight render to complete.
	l.waitForRender()
}
