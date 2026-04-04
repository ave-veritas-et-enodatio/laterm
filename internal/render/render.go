package render

import (
	"context"
	"fmt"
	"log/slog"
)

// MathType indicates whether the expression is inline ($...$) or block ($$...$$).
type MathType int

const (
	Inline MathType = iota
	Block
)

// Renderer converts a LaTeX expression into terminal-ready bytes.
// Implementations must be safe for concurrent use.
type Renderer interface {
	// Render converts a LaTeX expression to terminal output bytes.
	// ctx carries a deadline for the render operation.
	// maxWidth is the maximum output width in pixels (for Sixel rendering).
	// Renderers that don't use pixel dimensions may ignore this value.
	// Returns the rendered bytes or an error (caller falls back on error).
	Render(ctx context.Context, expr string, mathType MathType, maxWidth int) ([]byte, error)
}

// Capabilities represents what the terminal supports.
type Capabilities struct {
	SixelSupported bool
	WidthCells     int
	HeightCells    int
	WidthPixels    int
	HeightPixels   int
}

// SafeRenderer wraps a Renderer with panic recovery so that a panicking
// renderer returns an error instead of crashing the process.
type SafeRenderer struct {
	Inner  Renderer
	Logger *slog.Logger
}

func (s *SafeRenderer) Render(ctx context.Context, expr string, mathType MathType, maxWidth int) (data []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("renderer panic: %v", r)
			if s.Logger != nil {
				s.Logger.Error("renderer panic recovered", "error", r)
			}
		}
	}()
	return s.Inner.Render(ctx, expr, mathType, maxWidth)
}

// FallbackRenderer tries a primary renderer; on error, it falls back to a
// secondary renderer. This lets the system try Sixel first and fall back to
// Unicode when Sixel fails. The secondary is wrapped in a SafeRenderer so
// panics in the fallback path are recovered.
type FallbackRenderer struct {
	Primary   Renderer
	Secondary Renderer
	Logger    *slog.Logger
}

func (f *FallbackRenderer) Render(ctx context.Context, expr string, mathType MathType, maxWidth int) ([]byte, error) {
	result, primaryErr := f.Primary.Render(ctx, expr, mathType, maxWidth)
	if primaryErr == nil && len(result) > 0 {
		return result, nil
	}
	if f.Logger != nil {
		if primaryErr != nil {
			f.Logger.Debug("primary renderer failed, trying fallback",
				slog.String("error", primaryErr.Error()))
		} else {
			f.Logger.Debug("primary renderer returned empty, trying fallback")
		}
	}
	safe := &SafeRenderer{Inner: f.Secondary, Logger: f.Logger}
	return safe.Render(ctx, expr, mathType, maxWidth)
}

// Select returns the appropriate renderer based on terminal capabilities.
// If Sixel is supported and sixelRenderer is non-nil, returns a
// FallbackRenderer that tries Sixel first and falls back to Unicode.
// Otherwise returns the unicodeRenderer directly.
func Select(caps Capabilities, sixelRenderer, unicodeRenderer Renderer, logger *slog.Logger) Renderer {
	if caps.SixelSupported && sixelRenderer != nil {
		return &FallbackRenderer{
			Primary:   sixelRenderer,
			Secondary: unicodeRenderer,
			Logger:    logger,
		}
	}
	return &SafeRenderer{Inner: unicodeRenderer, Logger: logger}
}
