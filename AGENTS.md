# AGENTS.md — LaTerm

Guidance for agents (automated or human) working on this codebase.
**Read ARCHITECTURE.md first.** It is the authoritative design reference and
supersedes any inference you draw from code alone.

---

## Project Overview

LaTerm is a Claude Code sidecar file monitor written in Rust. It watches the
active conversation log for the current project and renders LaTeX math
expressions as inline images in a separate graphics-capable terminal window
(kitty graphics protocol, iTerm2 imgcat, or Sixel).

Run it from the project directory in a separate kitty, ghostty, iTerm2, WezTerm,
Windows Terminal, or any Sixel-capable terminal window:

```sh
cargo run --release
# or, if installed on PATH:
laterm [--log-file <PATH>] [--catch-up[=<MINS>]] [--help]
```

It spawns no child process. It derives the Claude Code log directory, tails
every `*.jsonl` conversation log, and for each new entry emits a sparse feed
to stdout: only each math expression rendered as an inline image, plus the
limited surrounding anchor text needed to contextualize it. Pass `--catch-up`
to first replay math from recent history before tailing begins. It also renders
expressions typed or pasted directly into its window.

Crate name: `laterm`. Source root: `src/`.

---

## Build and Run

```sh
make build    # debug build (cargo build)
make release  # optimized build → target/release/laterm
make test     # unit tests (cargo test)
make check    # type-check all three release targets (validates cfg flags)
make dist     # cross-build aarch64-apple-darwin, x86_64-unknown-linux-gnu,
              #   x86_64-pc-windows-gnu via cargo-zigbuild → dist/
make fmt      # cargo fmt
make lint     # cargo clippy --all-targets
make setup    # rustup targets + cargo-zigbuild (needs zig: brew install zig)
```

`make dist` cross-compiles every target from one host using `cargo-zigbuild`
(zig as the cross-linker) — rustc bundles no cross-linker, so this is what makes
Mac→Linux/Windows work. It uses the `-gnu` Windows triple (zigbuild can't do
`-msvc`; `-gnu` runs fine on Windows). Plain `cargo build` / `cargo test` /
`cargo build --release` still work. `.github/workflows/release.yml` instead
builds each target **natively** on its own OS runner on tags, so the released
Windows binary is `-msvc`. The binary name is `laterm`. Build outputs go to
`target/` only — never in the source tree.

The Go prototype under `prototype/` still builds with its own Makefile. It is
**not** part of the Rust build and must not be modified when working on the
Rust implementation. See the Prototype section below.

---

## Module Structure

```
src/
  main.rs      CLI entry point. Wires all modules. No child process.
  watch.rs     Polling tailer. *.jsonl files, tail-only, partial-line buffering.
  convo.rs     jsonl parser. Extracts text from user/assistant entries.
  mathscan.rs  LaTeX delimiter scanner. Returns Vec<Unit{before, segments, after}>.
  render.rs    PNG renderer. RaTeX parse→layout→display list→PNG pipeline.
  graphics.rs  Protocol selection: kitty (preferred), imgcat, or sixel.
  kitty.rs     kitty graphics protocol encoder. supported() detection.
  imgcat.rs    iTerm2 OSC 1337 encoder. supported() detection.
  sixel.rs     Sixel encoder. supported() via DA1 query (parse_da1).
  termbg.rs    OSC 11 background query + shared query_terminal() helper.
  logging.rs   Log configuration, file + stderr output, level management.
```

### Module responsibilities in brief

**`main`** — Wiring only. Parse CLI flags (`--log-file`, `--catch-up`,
`--help`). Initialize logging. Derive the Claude Code log directory for the
current working directory (`~/.claude/projects/<cwd-with-slashes-as-dashes>`).
Select a graphics protocol via `graphics::select` and exit immediately with an
error if none of kitty, imgcat, or Sixel is supported. Detect the terminal background
(`termbg::query`) and configure the renderer theme. Compute the block-height
threshold once (1.5× a reference render). If `--catch-up` was given, replay
math from conversation entries timestamped within the last N minutes (across
all `*.jsonl`, chronological order) before starting the tailer. Start the watch
loop. For each line, call `convo::extract` → `mathscan::scan`; for each `Unit`,
write its `before`/`after` anchor lines and walk its segments in order: text
inline, math rendered via `render::render` and emitted via the selected
protocol's `encode` (block, own line) when the rendered height ≥ the threshold,
else `encode_inline` (one text row). Handle SIGINT/SIGTERM. Also reads stdin
(line mode): each typed/pasted line is rendered through the same path —
delimited math like conversation text, an undelimited line as one bare LaTeX
expression. A shared output mutex keeps conversation and manual renders from
interleaving. Contains no rendering, parsing, or protocol logic. Only this
module writes to stdout.

**`watch`** — Polls `dir` at `interval` for `*.jsonl` files. Tail-only:
existing files are recorded at their current size at startup; files appearing
later start at offset 0. Emits complete `\n`-terminated lines. Buffers partial
lines. Resets to offset 0 on file truncation/rotation. Waits gracefully if
`dir` does not yet exist. Closes/drops the producer when cancellation is
signalled. No laterm module imports.

**`convo`** — Parses one jsonl line. Returns text content from `"user"` and
`"assistant"` entries only. `message.content` may be a JSON array of blocks
(returns `type:"text"` blocks) or a plain JSON string (returned as a single
item). Returns None/empty for all other cases, including parse failure. Never
panics. Uses `serde_json`.

**`mathscan`** — `scan(text: &str) -> Vec<Unit>`. Returns one
`Unit{before, segments, after}` per math-bearing line. `segments` interleaves
`Segment{kind: Text}` and `Segment{kind: Math}` in document order (for `Math`,
the field holds the inner expression and a `display: bool` for block vs
inline). All context is bounded to ≤40 runes (word-snapped, ellipsis). The
line's own leading/trailing text is trimmed so a long sentence does not echo in
full. `before`/`after` add the nearest prose from adjacent non-math lines
(crossing blank lines) only on a side with no same-line text — a display block
alone on its line reads in context; an inline formula in a sentence is anchored
by its own line. Delimiters: `$$...$$`/`\[...\]` (display); `$...$`/`\(...\)`
(inline). `\$` is a literal dollar. Unterminated/empty spans become literal
text. Multi-line display blocks supported. Two expressions on one line stay in
a single unit (no duplication). No-math lines are dropped. Returns empty vec if
no math. No laterm module imports.

**`render`** — Runs the RaTeX pipeline: `ratex_parser::parse` →
`ratex_layout::layout` + `to_display_list` → `ratex_render::render_to_png`.
RaTeX is synchronous and returns `Result` — no timeout is applied. Returns
`(png_bytes, height_px)` on success, `Err` on failure. Rejects PNGs exceeding
4096×4096 px (`ImageTooLarge`). Glyph color is set via
`LayoutOptions::with_color(Color)`; background via
`RenderOptions.background_color` (`Color { r, g, b, a: f32 }`; `a = 0.0` is
transparent). On any error, the caller passes raw LaTeX through as text.
Imports `ratex-parser`, `ratex-layout`, `ratex-render`, `ratex-types`. Does not
import `watch`, `convo`, `mathscan`, or `graphics`.

**`graphics`** — `select() -> Option<Protocol>`. Tries kitty (env-based),
then imgcat (env-based), then Sixel (DA1 tty round-trip — probed last). Returns
`None` when none is supported (main fails loud). Imports `kitty`, `imgcat`, and
`sixel`.

**`kitty`** — `supported() -> bool` (checks `KITTY_WINDOW_ID`, `TERM`
containing `kitty`/`ghostty`, `TERM_PROGRAM == "ghostty"`). `encode`/
`encode_inline` emit the kitty graphics protocol: PNG (`f=100`) in ≤4096-byte
base64 chunks via `\x1b_G…\x1b\\` APC escapes (`a=T`); inline adds `r=1`.
Uses `base64` crate. No laterm module imports.

**`imgcat`** — `supported() -> bool` (checks `TERM_PROGRAM == "iTerm.app"`,
`LC_TERMINAL == "iTerm2"`, `TERM_PROGRAM == "WezTerm"`). `encode(png: &[u8])
-> Vec<u8>` wraps in `ESC ] 1337 ; File=inline=1;size=<len>:<base64> BEL` at
native size; `encode_inline(png: &[u8]) -> Vec<u8>` adds
`height=1;preserveAspectRatio=1`. Uses `base64` crate. Does not write to
stdout. No laterm module imports.

**`sixel`** — `supported() -> bool` sends a DA1 query (`\x1b[c`) via
`termbg::query_terminal` and parses the `\x1b[?<attrs>c` reply with `parse_da1`;
returns true when attribute `4` is present. The shared `query_terminal` helper
handles raw-tty/Console API on both unix and Windows, so DA1 works on Windows
Terminal. `encode(png: &[u8]) -> Vec<u8>` decodes the PNG to RGBA8 with the
`png` crate, then emits a standard Sixel stream: DCS introducer + 1:1 raster
attributes, RGB color registers (0–100 scale), 6-row bands with RLE.  Colors
are collected directly from pixels (bit-dropping loop to cap at 256 registers) —
no quantization crate. `encode_inline` is identical to `encode` (Sixel has no
cell-scaling equivalent to kitty's `r=1`). Uses an opaque background for renders
(terminal color or white) rather than the transparent path kitty/imgcat use.

**`termbg`** — `query(timeout: Duration) -> Option<(u8, u8, u8)>` sends OSC 11
(`\x1b]11;?`) and parses the `rgb:…` reply; `is_dark` and `parse_osc11` are
platform-independent. `query_terminal(request, timeout) -> Option<String>` is a
shared low-level helper (pub(crate)) that sends any terminal request in raw mode
and returns the reply — reused by `sixel`'s DA1 probe. Platform implementations:
- **unix** — opens `/dev/tty`; uses `libc` for termios raw mode
  (`cfmakeraw`/`tcsetattr`) and `select(2)` for the read timeout.
- **Windows** — uses `windows-sys` Console API: `GetStdHandle`,
  `SetConsoleMode` with `ENABLE_VIRTUAL_TERMINAL_INPUT`,
  `WaitForSingleObject` for the timeout, `ReadConsoleA` for the reply.
  Full OSC 11 parity — not a stub.
Used once at startup to pick a contrasting glyph color.

**`logging`** — Configures structured, leveled logging. File output at mode
0600, append. Path resolved by `main` from `--log-file` or the default beside
the executable. Level controlled by `LATERM_LOG_LEVEL`. If the file cannot be
opened, prints one warning to stderr and disables logging for the run. Never
writes to stdout.

---

## Key Architecture Constraints

These are invariants from ARCHITECTURE.md. Violating any is a blocking defect.

### Isolation constraints

- **`watch`, `convo`, `mathscan`, `kitty`, and `imgcat` import no other laterm
  modules.** Each depends only on the standard library and (where noted) one
  external crate. `sixel` is the sole exception: it calls `termbg::query_terminal`
  for the DA1 probe.
- **Only `main` writes to stdout.** No other module may write to `std::io::stdout`.
  Logging goes to file or stderr.
- **RaTeX crates confined to `render`.** No other module imports
  `ratex-parser`, `ratex-layout`, `ratex-render`, or `ratex-types`.
- **Graphics protocols confined.** `kitty`, `imgcat`, and `sixel` are imported
  only by `graphics`; `main` picks one via `graphics::select`. `render` knows
  nothing of the output protocol — it only produces PNG bytes + height.

### Dependency direction

```
main
  |
  +---> watch
  +---> convo
  +---> mathscan
  +---> render  (ratex-parser, ratex-layout, ratex-render, ratex-types)
  +---> graphics --> kitty  (base64)
  |             |--> imgcat (base64)
  |             \--> sixel  (png; calls termbg::query_terminal)
  +---> termbg
  +---> logging

(all modules may use logging)
```

No cycles. Direction is strictly downward.

### Rendering errors are non-fatal

`render::render` returns `Err` for parse errors, render errors, and oversized
images (>4096×4096 px). In all cases `main` logs and passes the raw LaTeX
through (delimited) rather than dropping it, then continues to the next
segment. The process never exits due to a render failure.

### Graphics-only, fail loud (kitty → imgcat → Sixel)

There is no Unicode text fallback. `graphics::select` is a hard gate at startup:
it tries kitty (env-based), then imgcat (env-based), then Sixel (DA1 tty
round-trip — probed last because it requires a terminal query). Returns `None`
when none is supported, in which case laterm exits immediately with an error.
Glyph color contrasts the detected terminal background (OSC 11), with a
black-on-white fallback when detection fails. The Sixel path uses an opaque
background (terminal color or white) rather than the transparent path kitty/imgcat
use, because Sixel transparency is less universally honored.

### Inline vs block layout is decided by rendered height

`main` renders a reference capital letter once at startup and treats any
expression whose rendered height is ≥ 1.5× that as a block: newline +
`proto.encode(png)` + newline, on its own line at full size. Shorter expressions
use `proto.encode_inline`, staying in the text flow. For Sixel, `encode_inline`
is identical to `encode` (no cell-scaling equivalent). The threshold is computed
at runtime so it tracks RaTeX's actual output rather than a hard-coded pixel
count.

---

## RaTeX Integration

The math renderer is [RaTeX](https://github.com/erweixin/RaTeX), a pure-Rust,
KaTeX-compatible renderer. It is declared in `Cargo.toml` as four crates:
`ratex-parser`, `ratex-layout`, `ratex-render` (with `embed-fonts` feature),
and `ratex-types`.

The pipeline in `render.rs` is:
```
parse(expr: &str)  ->  ratex_parser::Ast
layout(&ast, &LayoutOptions)  ->  LBox
to_display_list(&lbox)  ->  DisplayList
render_to_png(&display_list, &RenderOptions)  ->  Vec<u8>
```

The `embed-fonts` feature on `ratex-render` bundles KaTeX fonts directly into
the binary. No external font directory is needed at runtime.

RaTeX returns `Result` on failure — it does not panic. This means no
`catch_unwind` or panic recovery is needed at the RaTeX boundary. Bad input
surfaces as `Err` and is handled by passing the raw expression through as text.

**What RaTeX fixes vs the Go prototype's go-latex renderer:**
- `\begin`/`\end` environments (matrices, `aligned`, etc.) render correctly.
- Operator-limit stacking (`\sum`, `\int` limits above/below in display mode).
- Accents (`\hat`, `\bar`, `\vec`, `\tilde`, …).
- Stretchy delimiters.
- There is no command allowlist — coverage is not a constraint.

---

## Testing

```sh
cargo test               # runs all unit tests
cargo test -- --nocapture # with stdout visible
```

The prototype's Go fuzz target (`FuzzCheck` in `prototype/internal/sanitize/`)
informed the mathscan edge-case list; Rust fuzz tests for `mathscan` and
`convo` are a good addition.

---

## Dependency Policy

Declared dependencies are listed in `Cargo.toml` and justified in
ARCHITECTURE.md section 2. Do not add a new dependency without updating that
table with a justification.

Current direct dependencies:

| Dependency | Used in |
|---|---|
| `ratex-parser` v0.1.9 | `render` |
| `ratex-layout` v0.1.9 | `render` |
| `ratex-render` v0.1.9 (`embed-fonts`) | `render` |
| `ratex-types` v0.1.9 | `render` |
| `serde` v1 | `convo` |
| `serde_json` v1 | `convo` |
| `base64` v0.22 | `graphics` |
| `png` v0.17 | `sixel` (PNG→RGBA decode; no sixel or quantization crate — encoder is in-house) |
| `chrono` v0.4 | `main` (RFC3339 timestamp parsing for `--catch-up`) |
| `ctrlc` v3 | `main` (cross-platform signal handling, MIT/Apache-2.0) |
| `libc` v0.2 | `termbg` (unix only — `[target.'cfg(unix)']`) |
| `windows-sys` v0.59 | `termbg` (Windows only — `[target.'cfg(windows)']`) |

---

## Logging

- **Always writes to a log file** (keeps the rendered feed clean). Default
  path: `laterm.log` beside the executable, falling back to cwd. Override with
  `--log-file <PATH>`. If the file cannot be opened, one warning goes to stderr
  and logging is disabled for the run.
- **Never write to stdout.** stdout is reserved exclusively for the rendered
  feed written by `main`.
- Log file is opened for append at mode 0600 (unix).
- Level set via `LATERM_LOG_LEVEL` (debug, info, warn, error). Default: info.
- Initialize logging early in `main` before any other work.

---

## Common Gotchas

### Log directory derivation

The log directory is `~/.claude/projects/<cwd>` where every `/`, `\`, and `:`
in the absolute working directory path is replaced by `-`. For example,
`/Users/benn/projects/laterm` becomes
`~/.claude/projects/-Users-benn-projects-laterm`, and on Windows
`C:\Users\benn\projects\laterm` becomes
`~/.claude/projects/C--Users-benn-projects-laterm`. This is the same convention
used by Claude Code; do not change it unilaterally.

### Missing log directory is not an error

If the derived log directory does not exist when laterm starts, the watcher
waits silently until it appears. This is normal when laterm is started before
Claude Code has opened the project.

### Render errors are non-fatal

`render::render` returns `Err` for RaTeX failures and oversized images
(>4096×4096 px). In all cases `main` logs and passes the raw LaTeX through
(delimited) rather than dropping it, then continues to the next segment.
The process never exits due to a render failure.

### Prototype under `prototype/`

The Go prototype under `prototype/` is the validated reference implementation.
It is kept for its behavioral specification value (the mathscan windowing logic,
kitty/imgcat escape sequences, OSC 11 query mechanics) — read it when behavior
details are unclear. Do not modify it when working on the Rust implementation.
It is not part of the Rust build.

---

## Prototype

`prototype/` contains the original Go implementation. It still builds with
`make build` from within that directory and can be run for behavioral
comparison. Its internal packages — especially `mathscan`, `kitty`, and
`imgcat` — are the reference for the corresponding Rust modules. The Go
implementation is not the shipped tool; `cargo build` in the repo root is the
authoritative build.
