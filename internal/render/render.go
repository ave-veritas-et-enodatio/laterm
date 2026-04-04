package render

import "context"

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
	// maxWidth is the maximum output width in terminal columns.
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

// Select returns the appropriate renderer based on terminal capabilities.
// If Sixel is supported and sixelRenderer is non-nil, returns it.
// Otherwise returns the unicodeRenderer.
func Select(caps Capabilities, sixelRenderer, unicodeRenderer Renderer) Renderer {
	if caps.SixelSupported && sixelRenderer != nil {
		return sixelRenderer
	}
	return unicodeRenderer
}
