# Handoff: Replace go-latex with KaTeX + wazero

## Decision

go-latex v0.2.0 is too incomplete for production use. It panics on `\mathcal`, `\mathbb`, `\left`/`\right`, `\text`, `\operatorname`, parentheses in math mode, and many other common LaTeX constructs. Patching individual failures is unsustainable — each new expression class hits a new `panic("not implemented")`.

Replace it with KaTeX running inside wazero (pure Go WASM runtime). KaTeX has near-complete LaTeX math coverage and is the standard for fast, accurate LaTeX math rendering.

## Current Architecture

```
LaTeX expr → sanitize → go-latex (mtex.Render) → PNG → png.DecodeConfig → Sixel encode
                                   ↑ PANICS HERE
```

Fallback chain: `FallbackRenderer{Primary: sixelRenderer, Secondary: unicodeRenderer}`

## Target Architecture

```
LaTeX expr → sanitize → KaTeX (WASM via wazero) → output → image conversion → Sixel encode
```

Same fallback chain. Unicode renderer stays as terminal fallback (no changes needed).

## Key Design Question: KaTeX Output → Image

KaTeX natively outputs **HTML+CSS markup**, not SVG or images. Getting from KaTeX output to a terminal-displayable image requires solving the HTML→image step. Options to evaluate in the design phase:

1. **KaTeX HTML → extract Unicode text**: Parse KaTeX's HTML output and extract the text content with positioning. This gives high-quality Unicode rendering (KaTeX handles all the math parsing) without needing image rendering. Would replace both go-latex AND the hand-rolled Unicode renderer for text output. Simple but produces no images.

2. **KaTeX HTML → headless render → PNG**: Use a minimal HTML/CSS layout engine (compiled to WASM or pure Go) to render KaTeX's HTML output to a bitmap. More complex but produces proper images.

3. **KaTeX MathML output → custom renderer**: KaTeX can output MathML via `renderToString(expr, {output: 'mathml'})`. Parse the MathML in Go and render it to an image using font metrics and Go's image libraries.

4. **KaTeX with SVGLike output**: Investigate whether any KaTeX configuration, wrapper, or post-processing step can produce rasterizable output (SVG or canvas commands) rather than HTML+CSS.

The design phase must evaluate these options and select one. The constraint is: must produce either (a) high-quality Unicode text or (b) a rasterizable image, from within a pure Go binary with CGO_ENABLED=0.

**Note**: If the HTML→image conversion proves intractable, MathJax is the alternative — it produces self-contained SVG output that can be rasterized directly. MathJax is larger (~1.5MB vs KaTeX's ~300KB) but eliminates the output format problem entirely. The design phase should prototype the KaTeX path first but have MathJax as a known-viable fallback.

## What Changes

### Must replace
- `internal/render/sixel/sixel.go` — the `latexToPNG()` method (lines 179-196)
  - Currently calls `mtex.Render()` and `drawimg.NewRenderer()`
  - Replace with: KaTeX WASM module → output processing → PNG
  - The rest of `renderPipeline()` stays: dimension check, Sixel encoding, panic recovery

### Must add
- **WASM module**: KaTeX compiled to WASM
  - Build-time: bundle KaTeX + JS wrapper → compile with javy → produce `katex.wasm`
  - Embed in binary via `//go:embed katex.wasm`
- **WASM runtime**: `github.com/tetratelabs/wazero` dependency (pure Go, no CGO)
- **Output processing**: Convert KaTeX output to image (approach TBD — see design question)
- **New package**: `internal/render/katex/` — manages WASM module lifecycle
- **Build tooling**: Makefile target to compile KaTeX to WASM (requires npm + javy at build time)

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
- `cmd/laterm/main.go` — wiring stays the same (swap renderer config at most)

## Rendering Pipeline Detail

### Build time (one-time, produces embedded artifact)

```
npm install katex
→ write JS wrapper:
    const katex = require('katex');
    // read LaTeX from stdin
    // output = katex.renderToString(input, { throwOnError: false, displayMode: ... })
    // write result to stdout
→ javy compile wrapper.js -o katex.wasm
→ go:embed katex.wasm
```

### Runtime (per expression)

1. **Sanitize** (existing) — allowlist check, depth budget, length limit
2. **KaTeX render** (new) — pass LaTeX string to WASM module, get HTML/MathML back
3. **Output → image** (new, design TBD) — convert KaTeX output to PNG
4. **Dimension check** (existing) — verify image fits terminal pixel bounds
5. **Sixel encode** (existing) — go-sixel encodes image.Image to Sixel bytes

### Error handling

- WASM module initialization failure → log error, use Unicode renderer only (graceful degradation)
- KaTeX render error → return error, FallbackRenderer cascades to Unicode
- Output conversion error → return error, cascade to Unicode
- Panic in any step → caught by existing `recover()` wrapper in `Render()` goroutine

## Constraints

| Constraint | Requirement |
|---|---|
| CGO_ENABLED=0 | wazero is pure Go. All processing must be pure Go or embedded WASM. |
| Single binary | WASM module embedded via `//go:embed`, not loaded from filesystem |
| Build-time deps | npm + javy needed to compile WASM (not needed by end users) |
| Binary size | KaTeX WASM will add ~3-8MB. Acceptable tradeoff for complete rendering. |
| Startup cost | WASM module compilation is slow (~100-500ms). Do it once, lazily, on first render. Cache the compiled module. |
| Render latency | KaTeX + output processing should complete within existing timeout budgets (2s inline, 5s block) |
| Concurrency | wazero module instances can be pooled. Current render semaphore (cap 4) bounds concurrency. |

## Terminal Compatibility (tested)

| Terminal | Sixel | Unicode fallback |
|---|---|---|
| iTerm2 v3.6.9 | Yes (confirmed with magick) | Yes |
| Zed terminal | No | Yes |
| macOS Terminal.app | No | Yes |

## Open Questions for Design Phase

1. **Output format strategy**: How to get from KaTeX's HTML+CSS output to a terminal image. This is the critical design decision — see "Key Design Question" section above.

2. **javy compatibility**: javy uses QuickJS internally, not V8. Verify KaTeX runs correctly under QuickJS. KaTeX's `renderToString` is pure computation (no DOM needed), so this should work, but needs verification.

3. **KaTeX vs MathJax fallback**: If KaTeX's HTML→image path proves too complex, MathJax can produce self-contained SVG directly. The design phase should evaluate both and make a final call. MathJax SVG uses `<path>` elements with embedded glyph data — no external fonts needed, directly rasterizable with a pure Go SVG renderer (e.g., oksvg+rasterx).

4. **WASM module lifecycle**: Initialize lazily on first render? Pre-initialize at startup? Pool instances for concurrent renders? wazero supports compiled module caching.

5. **KaTeX version**: Pin a specific version for reproducibility.

6. **Sanitizer scope**: With KaTeX handling rendering, the sanitizer's role shifts from "protect go-latex from panics" to "prevent resource exhaustion in WASM". The allowlist may be too restrictive for KaTeX (which can handle everything). Consider relaxing it, but keep length limits and nesting depth for DoS protection.

## Impact Summary

| Component | go-latex Impact | Action |
|---|---|---|
| Renderer Interface | None | Reuse as-is |
| Unicode Renderer | None | Reuse as-is |
| **Sixel Renderer** | `latexToPNG()` depends on go-latex | Replace with KaTeX WASM pipeline |
| Main wiring | None (indirect via sixel) | Reuse as-is |
| go.mod | Direct import + 5 transitive deps | Remove go-latex, add wazero |
| Makefile | None | Add WASM build target |
| Sanitizer | None | Reuse (consider relaxing allowlist) |
| Terminal detection | None | Reuse as-is |

## Files to Read Before Starting

- `internal/render/sixel/sixel.go` — the pipeline you're replacing part of
- `internal/render/render.go` — the interface contract
- `cmd/laterm/main.go` — wiring
- `ARCHITECTURE.md` — invariants (especially #1, #10, #11)
- `go.mod` — current dependency tree
