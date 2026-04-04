// Package sixel renders LaTeX math expressions as Sixel graphics.
//
// The entire pipeline — LaTeX parse, image render, Sixel encode — runs inside
// a goroutine with panic recovery. Any failure (including panics from the
// underlying go-latex or go-sixel libraries) is surfaced as an error so the
// caller can fall back to Unicode rendering.
package sixel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"

	"codeberg.org/go-latex/latex/drawtex/drawimg"
	"codeberg.org/go-latex/latex/mtex"
	sixelenc "github.com/mattn/go-sixel"

	"github.com/benn-herrera/laterm/internal/logging"
	"github.com/benn-herrera/laterm/internal/render"
)

// Sentinel errors.
var (
	ErrImageTooLarge = errors.New("rendered image exceeds terminal dimensions")
	ErrRenderPanic   = errors.New("panic during render")
)

// Default limits applied when Config fields are zero.
const (
	defaultMaxPixelWidth  = 800
	defaultMaxPixelHeight = 600
	defaultDPI            = 96.0
	defaultFontSize       = 12.0
)

// Config holds parameters for constructing a Renderer.
type Config struct {
	MaxPixelWidth  int
	MaxPixelHeight int
	DPI            float64
	Logger         *slog.Logger
}

// Renderer converts LaTeX math expressions to Sixel-encoded byte sequences.
type Renderer struct {
	maxPixelWidth  int
	maxPixelHeight int
	dpi            float64
	logger         *slog.Logger
}

// Compile-time interface check.
var _ render.Renderer = (*Renderer)(nil)

// New creates a Renderer with the given configuration.
// Zero-value fields in cfg are replaced with sensible defaults.
func New(cfg Config) *Renderer {
	r := &Renderer{
		maxPixelWidth:  cfg.MaxPixelWidth,
		maxPixelHeight: cfg.MaxPixelHeight,
		dpi:            cfg.DPI,
		logger:         cfg.Logger,
	}
	if r.maxPixelWidth <= 0 {
		r.maxPixelWidth = defaultMaxPixelWidth
	}
	if r.maxPixelHeight <= 0 {
		r.maxPixelHeight = defaultMaxPixelHeight
	}
	if r.dpi <= 0 {
		r.dpi = defaultDPI
	}
	if r.logger == nil {
		r.logger = logging.Default()
	}
	return r
}

// renderResult carries the output of the background render goroutine.
type renderResult struct {
	data []byte
	err  error
}

// Render converts a LaTeX expression into Sixel-encoded bytes.
//
// The entire pipeline runs in a separate goroutine with panic recovery.
// If ctx is cancelled before the pipeline completes, ctx.Err() is returned.
// maxWidth from the interface is used to cap image width if it is smaller
// than the configured MaxPixelWidth.
func (r *Renderer) Render(ctx context.Context, expr string, mathType render.MathType, maxWidth int) ([]byte, error) {
	// Fast path: already cancelled.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	widthLimit := r.maxPixelWidth
	if maxWidth > 0 && maxWidth < widthLimit {
		widthLimit = maxWidth
	}

	ch := make(chan renderResult, 1)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- renderResult{err: fmt.Errorf("%w: %v", ErrRenderPanic, rec)}
			}
		}()

		data, err := r.renderPipeline(expr, mathType, widthLimit)
		ch <- renderResult{data: data, err: err}
	}()

	select {
	case res := <-ch:
		return res.data, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// renderPipeline executes the parse → render-to-image → check-size → sixel-encode
// sequence. Called only from within the recovery goroutine.
func (r *Renderer) renderPipeline(expr string, mathType render.MathType, widthLimit int) ([]byte, error) {
	// Step 1: Render LaTeX to a compressed PNG in memory via go-latex.
	pngData, err := r.latexToPNG(expr, mathType)
	if err != nil {
		return nil, fmt.Errorf("latex render: %w", err)
	}

	// Step 2: Check image dimensions from the PNG header only — this reads
	// a few dozen bytes and does NOT allocate the full decompressed bitmap.
	cfg, err := png.DecodeConfig(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("png header: %w", err)
	}

	if cfg.Width > widthLimit || cfg.Height > r.maxPixelHeight {
		r.logger.Warn("rendered image exceeds size limits",
			slog.Int("img_width", cfg.Width),
			slog.Int("img_height", cfg.Height),
			slog.Int("max_width", widthLimit),
			slog.Int("max_height", r.maxPixelHeight),
		)
		return nil, ErrImageTooLarge
	}

	// Step 3: Full decode — dimensions are safe, allocate the bitmap.
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("png decode: %w", err)
	}

	// Step 4: Encode image as Sixel.
	sixelBytes, err := r.encodeToSixel(img)
	if err != nil {
		return nil, fmt.Errorf("sixel encode: %w", err)
	}

	r.logger.Debug("sixel render complete",
		slog.Int("img_width", cfg.Width),
		slog.Int("img_height", cfg.Height),
		slog.Int("sixel_bytes", len(sixelBytes)),
	)

	return sixelBytes, nil
}

// latexToPNG parses the LaTeX expression and renders it to compressed PNG bytes.
//
// go-latex's drawimg.Renderer writes PNG to an io.Writer. We return the raw
// PNG so the caller can inspect dimensions cheaply via png.DecodeConfig before
// committing to a full decode.
func (r *Renderer) latexToPNG(expr string, mathType render.MathType) ([]byte, error) {
	// The go-latex parser expects the expression wrapped in math delimiters.
	// Ensure the expression is properly delimited.
	wrapped := wrapExpr(expr, mathType)

	var pngBuf bytes.Buffer
	dst := drawimg.NewRenderer(&pngBuf)

	// mtex.Render parses the expression, lays out the TeX boxes, and calls
	// dst.Render with the computed width/height (in inches) and DPI.
	// Passing nil for fonts uses the built-in Go fonts.
	err := mtex.Render(dst, wrapped, defaultFontSize, r.dpi, nil)
	if err != nil {
		return nil, fmt.Errorf("mtex.Render: %w", err)
	}

	return pngBuf.Bytes(), nil
}

// wrapExpr ensures the expression has appropriate LaTeX math delimiters.
// go-latex's parser requires $ or $$ delimiters around math content.
func wrapExpr(expr string, mathType render.MathType) string {
	// If the expression already has delimiters, return as-is.
	if len(expr) >= 2 && expr[0] == '$' && expr[len(expr)-1] == '$' {
		return expr
	}

	switch mathType {
	case render.Block:
		// go-latex doesn't support $$ directly in the same way;
		// use single $ which triggers math mode.
		return "$" + expr + "$"
	default:
		return "$" + expr + "$"
	}
}

// encodeToSixel converts an image.Image to Sixel-encoded bytes.
func (r *Renderer) encodeToSixel(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := sixelenc.NewEncoder(&buf)
	err := enc.Encode(img)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
