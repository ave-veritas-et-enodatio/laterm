# Handoff: Replace go-latex with MathJax + wazero

## Decision

go-latex v0.2.0 is too incomplete for production use. It panics on `\mathcal`, `\mathbb`, `\left`/`\right`, `\text`, `\operatorname`, parentheses in math mode, and many other common LaTeX constructs. Patching individual failures is unsustainable — each new expression class hits a new `panic("not implemented")`.

Replace it with MathJax running inside wazero (pure Go WASM runtime). MathJax has near-complete LaTeX math coverage and produces self-contained SVG output that can be rasterized to PNG in pure Go.

## Current Architecture

```
LaTeX expr → sanitize → go-latex (mtex.Render) → PNG → png.DecodeConfig → Sixel encode
                                   ↑ PANICS HERE
```

Fallback chain: `FallbackRenderer{Primary: sixelRenderer, Secondary: unicodeRenderer}`

## Target Architecture

```
LaTeX expr → sanitize → MathJax (WASM via wazero) → SVG → rasterize (pure Go) → PNG → Sixel encode
```

Same fallback chain. Unicode renderer stays as terminal fallback (no changes needed).

## What Changes

### Must replace
- `internal/render/sixel/sixel.go` — only the `latexToPNG()` method (lines 179-196)
  - Currently calls `mtex.Render()` and `drawimg.NewRenderer()`
  - Replace with: call MathJax WASM module → get SVG → rasterize SVG to PNG
  - The rest of `renderPipeline()` stays: PNG dimension check, Sixel encoding, panic recovery

### Must add
- **WASM module**: MathJax's SVG renderer compiled to WASM
  - Build-time: bundle MathJax + JS wrapper → compile with javy → produce `mathjax.wasm`
  - Embed in binary via `//go:embed mathjax.wasm`
- **WASM runtime**: `github.com/tetratelabs/wazero` dependency (pure Go, no CGO)
- **SVG rasterizer**: Pure Go SVG → PNG conversion (e.g., oksvg + rasterx, or similar)
- **New package**: `internal/render/mathjax/` — manages WASM module lifecycle, provides `latexToSVG()` function
- **Build tooling**: Makefile target or script to compile MathJax to WASM (requires npm + javy at build time)

### Must remove
- `codeberg.org/go-latex/latex v0.2.0` from go.mod
- ~5 transitive dependencies (gg, freetype, quant, x/image, x/text — verify none are used elsewhere)

### No changes needed
- `internal/render/render.go` — Renderer interface, FallbackRenderer, Select()
- `internal/render/unicode/` — entire package (terminal fallback)
- `internal/sanitize/` — entire package (pre-render validation)
- `internal/statemachine/` — entire package (LaTeX detection)
- `internal/stream/` — entire package (orchestration)
- `internal/pty/` — entire package (PTY management)
- `internal/termcap/` — entire package (terminal detection)
- `internal/logging/` — entire package
- `cmd/laterm/main.go` — wiring stays the same (swap sixel.New config at most)

## Rendering Pipeline Detail

### Build time (one-time, produces embedded artifact)

```
npm install mathjax-full
→ write JS wrapper:
    import { mathjax } from 'mathjax-full/js/mathjax.js'
    import { TeX } from 'mathjax-full/js/input/tex.js'
    import { SVG } from 'mathjax-full/js/output/svg.js'
    // read LaTeX from stdin, write SVG to stdout
→ javy compile wrapper.js -o mathjax.wasm
→ go:embed mathjax.wasm
```

### Runtime (per expression)

1. **Sanitize** (existing) — allowlist check, depth budget, length limit
2. **MathJax render** (new) — pass LaTeX string to WASM module, get SVG string back
3. **SVG → PNG** (new) — parse SVG, rasterize to image.RGBA at target DPI
4. **Dimension check** (existing) — verify PNG fits terminal pixel bounds
5. **Sixel encode** (existing) — go-sixel encodes image.Image to Sixel bytes

### Error handling

- WASM module initialization failure → log error, use Unicode renderer only (graceful degradation)
- MathJax render error → return error, FallbackRenderer cascades to Unicode
- SVG parse/rasterize error → return error, cascade to Unicode
- Panic in any step → caught by existing `recover()` wrapper in `Render()` goroutine

## Constraints

| Constraint | Requirement |
|---|---|
| CGO_ENABLED=0 | wazero is pure Go. SVG rasterizer must be pure Go. |
| Single binary | WASM module embedded via `//go:embed`, not loaded from filesystem |
| Build-time deps | npm + javy needed to compile WASM (not needed by end users) |
| Binary size | MathJax WASM will add ~5-10MB. Acceptable tradeoff for complete rendering. |
| Startup cost | WASM module compilation is slow (~100-500ms). Do it once, lazily, on first render. Cache the compiled module. |
| Render latency | MathJax + SVG rasterize should complete within existing timeout budgets (2s inline, 5s block) |
| Concurrency | wazero module instances can be pooled. Current render semaphore (cap 4) bounds concurrency. |

## Terminal Compatibility (tested)

| Terminal | Sixel | Unicode fallback |
|---|---|---|
| iTerm2 v3.6.9 | Yes (confirmed with magick) | Yes |
| Zed terminal | No | Yes |
| macOS Terminal.app | No | Yes |

## Open Questions for Design Phase

1. **javy vs alternatives**: javy compiles JS to WASM. Alternatives: emscripten, wasm-pack. javy is simplest for pure JS. Verify MathJax works under javy's QuickJS runtime (not V8 — some MathJax features may need polyfills).

2. **SVG rasterizer choice**: Need a pure Go SVG rasterizer that handles MathJax's SVG output (paths, text, transforms). Candidates: oksvg+rasterx, or canvaskit compiled to WASM (heavier). MathJax SVG uses `<path>` elements with glyph data — verify the rasterizer handles these.

3. **Font embedding**: MathJax's SVG output embeds glyph paths directly (no external font files needed). Verify this is the case with the SVG output mode we'll use.

4. **WASM module lifecycle**: Initialize lazily on first render? Pre-initialize at startup? Pool instances for concurrent renders? wazero supports compiled module caching.

5. **MathJax version**: mathjax-full v3.x is the current generation. Pin a specific version for reproducibility.

6. **Sanitizer scope**: With MathJax handling rendering, the sanitizer's role shifts from "protect go-latex from panics" to "prevent resource exhaustion in WASM". The allowlist may be too restrictive for MathJax (which can handle everything). Consider relaxing it, but keep length limits and nesting depth for DoS protection.

## Files to Read Before Starting

- `internal/render/sixel/sixel.go` — the pipeline you're replacing part of
- `internal/render/render.go` — the interface contract
- `cmd/laterm/main.go` — wiring
- `ARCHITECTURE.md` — invariants (especially #1, #10, #11)
- `go.mod` — current dependency tree
