// Copyright ©2020 The go-latex Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package drawimg implements a canvas for img.
package drawimg // import "github.com/ave-veritas-et-enodatio/laterm/internal/golatex/drawtex/drawimg"

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"

	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/drawtex"
	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/mtex"
	"git.sr.ht/~sbinet/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

type Renderer struct {
	w io.Writer
}

func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// maxPixelDim is the largest width or height (in pixels) we allow for a
// rendered bitmap.  4096 px at 4 bytes/pixel ≈ 64 MB worst-case, which is
// large but bounded.  Anything beyond this is almost certainly a degenerate
// box-model output (e.g. \hspace{999}).
const maxPixelDim = 4096

func (r *Renderer) Render(width, height, dpi float64, c *drawtex.Canvas) error {
	var (
		w = int(math.Ceil(width * dpi))
		h = int(math.Ceil(height * dpi))
	)

	if w > maxPixelDim || h > maxPixelDim {
		return fmt.Errorf("rendered image too large: %dx%d exceeds %d pixel limit", w, h, maxPixelDim)
	}

	ctx := gg.NewContext(w, h)
	// log.Printf("write: w=%g, h=%g", w, h)

	if false {
		draw.Draw(ctx.Image().(draw.Image), ctx.Image().Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	}

	ctx.SetColor(color.Black)

	for _, op := range c.Ops() {
		switch op := op.(type) {
		case drawtex.GlyphOp:
			drawGlyph(ctx, dpi, op)
		case drawtex.RectOp:
			drawRect(ctx, dpi, op)
		default:
			panic(fmt.Errorf("unknown drawtex op %T", op))
		}
	}

	return png.Encode(r.w, ctx.Image())
}

func drawGlyph(ctx *gg.Context, dpi float64, op drawtex.GlyphOp) {
	face, err := opentype.NewFace(op.Glyph.Font, &opentype.FaceOptions{
		DPI:     dpi,
		Size:    op.Glyph.Size,
		Hinting: font.HintingNone,
	})
	if err != nil {
		panic(fmt.Errorf("could not open font face for glyph %q: %+v",
			op.Glyph.Symbol, err,
		))
	}
	defer face.Close()
	ctx.SetFontFace(face)

	dpi /= 72

	x := op.X * dpi
	y := op.Y * dpi
	//	log.Printf("draw-glyph: %q w=%g, h=%g x=%g, y=%g, size=%v",
	//		op.Glyph.Symbol,
	//		w, h, x, y, op.Glyph.Size,
	//	)
	ctx.DrawString(op.Glyph.Symbol, x, y)
}

func drawRect(ctx *gg.Context, dpi float64, op drawtex.RectOp) {
	dpi /= 72
	ctx.NewSubPath()
	ctx.MoveTo(op.X1*dpi, op.Y1*dpi)
	ctx.LineTo(op.X2*dpi, op.Y1*dpi)
	ctx.LineTo(op.X2*dpi, op.Y2*dpi)
	ctx.LineTo(op.X1*dpi, op.Y2*dpi)
	ctx.LineTo(op.X1*dpi, op.Y1*dpi)
	ctx.ClosePath()
	ctx.Fill()
	//	log.Printf("draw-rect: pt1=(%g, %g) -> (%g, %g)", op.X1, op.Y1, op.X2, op.Y2)
}

var (
	_ mtex.Renderer = (*Renderer)(nil)
)
