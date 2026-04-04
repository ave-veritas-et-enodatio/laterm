# What Was Asked

Vendor go-latex v0.2.0 into the LaTerm repository and fix the panic sites that
cause common LaTeX constructs to crash the Sixel renderer. The external
`codeberg.org/go-latex/latex` module dependency was to be removed and replaced
with an in-repo copy that could be patched directly.

---

# What Changed

## Vendoring go-latex

go-latex v0.2.0 source (42 Go files) was copied into `internal/golatex/`. All
import paths were rewritten from `codeberg.org/go-latex/latex/...` to
`github.com/benn-herrera/laterm/internal/golatex/...`. The external module entry
was removed from `go.mod`. The font dependencies (`codeberg.org/go-fonts/*`,
`golang.org/x/image`) remain as external deps — they are consumed by the
vendored font backend and cannot be inlined.

`internal/render/sixel/sixel.go` was updated to import from the new internal
path. No other package imports go-latex.

## Panic site fixes across the parser and handler layers

Approximately 50 panic sites were converted to graceful degradation across five
files. The fixes fall into four categories:

**Macro registry gaps** — `macros.go` and `mtex/macros.go` were missing entries
for ~20 commonly used commands: `\mathcal`, `\mathbb`, `\mathbf`, `\mathit`,
`\text`, `\operatorname`, `\hat`, `\bar`, `\tilde`, `\vec`, `\dot`, `\ddot`,
`\acute`, `\grave`, `\breve`, `\check`, `\widetilde`, `\widehat`, `\left`,
`\right`. Both registries were extended. Because there are two parallel macro
registries (parser level and mtex/handler level), both required matching
argument signatures. A mismatch between the two causes silent mis-rendering or
a runtime panic.

**Handler dispatch gaps** — `mtex/parser.go` had missing cases for `ast.Sup`,
`ast.Sub`, `handleLeftRight`, and `handleStyleSwitch`. These were added with
correct dispatch logic. Seven additional panic-on-unknown-token sites were
converted to log-and-continue degradation.

**Nil registration bugs** — `\genfrac` had a nil function pointer in both
registries; it was removed from both. `\exp` had a mismatched signature between
the parser and mtex layers; the signature was corrected.

**Font backend** — `font/ttf/ttf.go` gained a `fontFallback` map that resolves
font types like `"cal"`, `"bb"`, and `"frak"` to the nearest available font
when the exact type is not loaded.

## Resource caps added during review

Three resource caps were added as review findings:

- `drawtex/drawimg/drawimg.go`: bitmap allocation capped at `maxPixelDim=4096`
  pixels per side. Expressions that would produce a larger image return an error
  instead of allocating.
- `mtex/parser.go`: comma-ok guards added to all dedicated handler dispatch
  sites to prevent panics on missing map entries.
- `render/render.go`: `SafeRenderer` wrapper type added; `Select()` now wraps
  all renderers in `SafeRenderer` before returning them. This ensures the
  Unicode-only path (when Sixel is unavailable) has its own `recover()` and
  cannot crash the process.

## Parser recursion and render depth limits

`internal/golatex/parser.go` gained a depth counter (`maxParseDepth=100`).
`internal/render/unicode/unicode.go` gained a depth parameter on `renderExpr`
(`maxRenderDepth=100`). Both return graceful errors at the limit.

## Sanitizer rebuilt

`internal/sanitize/allowlist.go` was rebuilt from the handler sources. Commands
without a working handler were removed. `\begin`/`\end` environment support was
removed from both the allowlist and the sanitizer logic because the parser
panics on `\begin`. The mismatch between what the sanitizer allowed and what the
parser could handle was a Critical finding in review; removing the unreachable
code resolves it.

## Test suite

`internal/golatex/mtex/fixes_test.go` was written with 22 expressions covering
the constructs that previously panicked. 21 pass. 1 (`mle`, a bare `|` token)
is annotated `stillPanics: true` — no handler exists yet. The test file uses a
`stillPanics` flag per case: `false` asserts no panic, `true` skips the render
and records a known failure. `TestStillPanics_Smoke` catches stale `true`
annotations if a handler is later added without flipping the flag.

---

# Design Decisions

**Two parallel macro registries must stay in sync.** The go-latex architecture
separates command recognition (parser level, `macros.go`) from command rendering
(handler level, `mtex/macros.go`). A command missing from either registry causes
failure. This is documented in AGENTS.md as an explicit maintenance rule.

**Sanitizer allowlist is derived from handler existence, not from the LaTeX
spec.** The allowlist only contains commands that have verified, working
handlers. This is more restrictive than the full LaTeX subset but avoids the
scenario where a command passes the sanitizer and then panics the parser. The
accepted tradeoff: some valid, harmless LaTeX is rejected.

**`\begin`/`\end` removed rather than partially fixed.** The parser panics on
`\begin`. The sanitizer had allowed it. Rather than add a sanitizer-level
workaround for a parser bug, both the allowlist entry and the sanitizer branch
were removed. Re-enabling environments requires parser-level work.

**`font/ttf` panics deferred.** Ten panic sites in `font/ttf/ttf.go` require
changing the `font.Backend` interface to return errors. That is a larger
refactor. These panics are currently caught by the sixel renderer's `recover()`
and cascade to Unicode fallback. No new panics were added; the existing ones are
documented and tracked.

**KaTeX + wazero replacement planned.** The vendoring and patching work is an
interim measure. A full replacement of go-latex with KaTeX running under wazero
is designed in `.claude/katex_rendering.md`. That path will resolve the
remaining handler gaps more completely.

---

# Security Findings

The security review in iteration 1 flagged three Critical items and five
Warnings. All were resolved:

- `\genfrac` nil registration — could panic on any expression using `\genfrac`.
  Fixed by removing the nil entry from both registries.
- `\begin`/`\end` sanitizer/parser mismatch — sanitizer allowed environments
  the parser could not handle. Fixed by removing them from both layers.
- `\exp` signature disagreement between parser and mtex layer — could cause
  wrong argument count and panic. Fixed by aligning the signatures.
- Parser recursion depth (Warning) — unbounded recursion on deeply nested
  input. Fixed with `maxParseDepth=100`.
- Unicode renderer depth (Warning) — same. Fixed with `maxRenderDepth=100`.
- Remaining mtex panics (Warning) — addressed by handler dispatch fixes and
  comma-ok guards.
- `FallbackRenderer` without `recover()` on the standalone Unicode path
  (Warning) — fixed by `SafeRenderer` wrapping all renderers returned by
  `Select()`.
- Sanitizer audit (Warning) — resolved by rebuilding the allowlist from handler
  sources.

The adversarial review (Phase 3b) found three more Warnings, all fixed:

- Unbounded bitmap allocation — fixed by `maxPixelDim=4096` cap in
  `drawimg.go`.
- Unicode-only path unrecovered — fixed by `SafeRenderer` wrapping all
  renderers returned by `Select()`.
- Unguarded handler Args access — fixed by comma-ok guards throughout
  `mtex/parser.go`.

No new attack surface was introduced. The session produced no unresolved
security findings.

---

# What to Review

**`internal/golatex/font/ttf/ttf.go`** — Ten panic sites remain. They are
currently caught by the sixel renderer's `recover()`. Decide whether the
`font.Backend` interface change needed to fix them is worth doing before the
KaTeX replacement, or whether it should be deferred to that milestone.

**`internal/sanitize/allowlist.go`** — The allowlist was rebuilt from handler
sources. Verify that the set of allowed commands matches your expectations for
what the Sixel renderer can handle. Commands not in this list are rejected before
reaching go-latex; too-narrow an allowlist degrades user experience for valid
expressions.

**`internal/golatex/mtex/fixes_test.go` — the `mle` case** — The bare `|`
token test is annotated `stillPanics: true`. If norm notation (`|x|`) is common
in practice, adding a `|` handler would be the next concrete improvement within
the go-latex layer.

**`render/render.go` — `SafeRenderer`** — Review the wrapper implementation to
confirm the `recover()` behavior matches your expectations for what gets logged
versus silently swallowed on fallback.

---

# Unresolved Items

1. `font/ttf/ttf.go` has 10 panic sites that require a `font.Backend` interface
   change to fix properly. Deferred pending the KaTeX replacement decision.
2. `\begin`/`\end` environment support is removed from the sanitizer.
   Re-enabling it requires parser-level redesign.
3. `parser.expect()` panics are invariant assertions not recoverable at the
   parser level without an error-accumulation refactor. They are caught by the
   sixel renderer's `recover()`.
4. The `mle` test expression (bare `|` token) is skipped — no handler exists.
5. Upstream go-latex code style issues (`interface{}` to `any`, `maps.Copy`)
   were not addressed. Cosmetic only; no functional impact.
