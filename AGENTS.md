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
laterm [--log <PATH>] [--catch-up[=<MINS>]] [--help]
```

It spawns no child process. It derives the Claude Code log directory, tails
every `*.jsonl` conversation log, and for each new entry echoes the full
conversation text to stdout with all LaTeX math expressions rendered as inline
images in place. Pass `--catch-up` to first replay recent history before
tailing begins. You can also paste text directly into its window to render it on the spot.

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
  mathscan.rs  LaTeX delimiter scanner. Returns flat Vec<Segment>, document order.
  render.rs    PNG renderer. RaTeX parse→layout→display list→PNG pipeline.
  graphics.rs  Protocol selection: kitty (preferred), imgcat, or sixel.
  kitty.rs     kitty graphics protocol encoder. supported() detection.
  imgcat.rs    iTerm2 OSC 1337 encoder. supported() detection.
  sixel.rs     Sixel encoder. supported() via DA1 query (parse_da1).
  termbg.rs    OSC 11 background query + shared query_terminal() helper.
  logging.rs   Log configuration, file + stderr output, level management.
```

### Module responsibilities in brief

**`main`** — Wiring only. Parse CLI flags (`--log`, `--catch-up`,
`--help`). Initialize logging. Derive the Claude Code log directory for the
current working directory (`~/.claude/projects/<cwd-with-slashes-as-dashes>`).
Select a graphics protocol via `graphics::select` and exit immediately with an
error if none of kitty, imgcat, or Sixel is supported. Detect the terminal background
(`termbg::query`) and configure the renderer theme. Render a reference capital
"X" once (`render::render("X", false)`, default 42 px if it fails); this value
drives both the block threshold (1.5×) and the proportional row count for every
image. If `--catch-up` was given, replay
math from conversation entries timestamped within the last N minutes (across
all `*.jsonl`, chronological order) before starting the tailer. Start the watch
loop. For each line, call `convo::extract` → `mathscan::scan`; walk the flat
segment list in order: text segments echoed verbatim (newlines preserved), math
segments rendered via `render::render`, then `rows = max(1, round(height_px /
reference_X_height_px × 1.25))` computed and `proto.encode(png, rows)` called for
every image (`1.25` = `ROW_SCALE`, a legibility bump). Height ≥ 1.5× reference → block layout (newline + image +
newline); otherwise inline (in flow). Handle SIGINT/SIGTERM. Also
reads stdin in raw mode via `termbg::raw_input()` (echo OFF, canonical mode OFF,
`ISIG` preserved). Enables bracketed paste (`ESC[?2004h`) at startup and
disables it (`ESC[?2004l`) on exit. Pasted text is captured silently between
the bracketed-paste markers `ESC[200~` … `ESC[201~` and processed through the
same conversation path — prose mirrored verbatim, delimited math rendered in
place. Pressing Enter/Return writes a visual separator to stdout: a blank line,
a terminal-width rule of `=` characters (width from `termbg::term_width()`,
default 80 columns), then a newline. Debounce: if the last output was already a
manual separator, Enter beeps instead of stacking a second rule; any rendered
content (conversation entry or paste) re-arms it. Typed printable keystrokes
produce a throttled BEL (`\x07`, at most ~once per 250 ms); escape sequences
and control bytes are consumed silently. Typed input is never rendered; the old
bare-expression behavior is removed. A shared output mutex keeps conversation
and manual renders from interleaving. Contains no rendering, parsing, or
protocol logic. Only this module writes to stdout (bracketed-paste toggles,
separator rules, and BEL included).

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

**`mathscan`** — `scan(text: &str) -> Vec<Segment>`. Returns the full input as
a flat, document-order interleaving of `Text(String)` and
`Math { expr: String, display: bool }` segments, with newlines preserved. No
trimming, no anchor windowing, no neighbor-context logic — every character of
the input appears in exactly one segment. Delimiters: `$$...$$`/`\[...\]`
(display); `$...$`/`\(...\)` (inline). `\$` is a literal dollar. Unterminated
and empty spans become literal text. Multi-line display blocks supported. Two
expressions on one line appear as two `Math` segments in order, interleaved
with the text between them. Text on lines with no math is emitted verbatim as
`Text` segments — nothing is dropped. Returns an empty vec only for empty
input. No laterm module imports.

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
containing `kitty`/`ghostty`, `TERM_PROGRAM == "ghostty"`).
`encode(png: &[u8], rows: u32)` emits the kitty graphics protocol: PNG
(`f=100`) in ≤4096-byte base64 chunks via `\x1b_G…\x1b\\` APC escapes,
with `a=T,f=100,r=<rows>` — `rows` is always present and is the proportional
row count from `main`. Uses `base64` crate. No laterm module imports.

**`imgcat`** — `supported() -> bool` (checks `TERM_PROGRAM == "iTerm.app"`,
`LC_TERMINAL == "iTerm2"`, `TERM_PROGRAM == "WezTerm"`).
`encode(png: &[u8], rows: u32) -> Vec<u8>` emits
`ESC ] 1337 ; File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len>:<base64> BEL`,
where `rows` is the proportional terminal-cell row count from `main`.
Uses `base64` crate. Does not write to stdout. No laterm module imports.

**`sixel`** — `supported() -> bool` sends a DA1 query (`\x1b[c`) via
`termbg::query_terminal` and parses the `\x1b[?<attrs>c` reply with `parse_da1`;
returns true when attribute `4` is present. The shared `query_terminal` helper
handles raw-tty/Console API on both unix and Windows, so DA1 works on Windows
Terminal. `encode(png: &[u8]) -> Vec<u8>` decodes the PNG to RGBA8 with the
`png` crate, then emits a standard Sixel stream: DCS introducer + 1:1 raster
attributes, RGB color registers (0–100 scale), 6-row bands with RLE.  Colors
are collected directly from pixels (bit-dropping loop to cap at 256 registers) —
no quantization crate. `encode(png: &[u8], rows: u32)`: the `rows` parameter is
currently **ignored** — Sixel has no cell-based row scaling, so the image renders
at its native pixel size (known limitation). Uses an opaque background for renders
(terminal color or white) rather than the transparent path kitty/imgcat use.

**`termbg`** — `query(timeout: Duration) -> Option<(u8, u8, u8)>` sends OSC 11
(`\x1b]11;?`) and parses the `rgb:…` reply; `is_dark` and `parse_osc11` are
platform-independent. `query_terminal(request, timeout) -> Option<String>` is a
shared low-level helper (pub(crate)) that sends any terminal request in raw mode
and returns the reply — reused by `sixel`'s DA1 probe. `raw_input() ->
Option<RawInput>` opens a persistent raw-input mode for `main`'s read side:
echo OFF, canonical mode OFF, `ISIG` preserved; RAII restores the prior mode on
drop. `term_width() -> usize` returns the current terminal width in columns, or
80 when it cannot be determined; unix uses `ioctl(STDOUT_FILENO, TIOCGWINSZ)`
via `libc`, Windows uses `GetConsoleScreenBufferInfo` via `windows-sys`.
Platform implementations:
- **unix** — `query_terminal` opens `/dev/tty`; uses `libc` for termios raw
  mode (`cfmakeraw`/`tcsetattr`) and `select(2)` for the read timeout.
  `raw_input` operates on stdin (fd 0); clears `ECHO | ICANON | IEXTEN`,
  keeps `OPOST` and `ISIG`; sets `VMIN=0 / VTIME=1`.
- **Windows** — uses `windows-sys` Console API: `GetStdHandle`,
  `SetConsoleMode` with `ENABLE_VIRTUAL_TERMINAL_INPUT`,
  `WaitForSingleObject` for the timeout, `ReadConsoleA` for the reply.
  `raw_input` operates on conin; clears `ENABLE_LINE_INPUT | ENABLE_ECHO_INPUT`,
  sets `ENABLE_VIRTUAL_TERMINAL_INPUT`; leaves output mode untouched.
  Full OSC 11 parity — not a stub.
Used once at startup to pick a contrasting glyph color (`query`); `raw_input`
is used by `main`'s manual-input read loop.

**`logging`** — Configures structured, leveled logging. Logging is opt-in:
path is resolved by `main` from `--log` only (no default). When a path is
given, file output at mode 0600, append. Acquires an exclusive writer lock for
the process lifetime (unix: advisory `flock(LOCK_EX | LOCK_NB)` via `libc`;
windows: `share_mode(FILE_SHARE_READ)` via std `OpenOptionsExt` — no new
dependency). A second instance that fails to acquire the lock prints one warning
to stderr and disables logging for that instance; readers (e.g. `tail -f`) are
unaffected. Level controlled by `LATERM_LOG_LEVEL` when logging is enabled.
If the file cannot be opened, prints one warning to stderr and disables logging
for the run. Never writes to stdout.

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

`main` renders a reference capital "X" once at startup (`render::render("X",
false)`, default 42 px if it fails). This single reference value drives two
independent decisions:

1. **Layout** — expressions whose height is ≥ 1.5× the reference are laid out
   as **block** (newline + image + newline, on its own line). Shorter
   expressions stay **inline** (in the text flow).
2. **Size** — for every image, regardless of layout,
   `rows = max(1, round(png_height_px / reference_X_height_px × 1.25))` is passed
   to `proto.encode(png, rows)`, so displayed height scales proportionally to the
   terminal's text size. The `1.25` factor (`ROW_SCALE`) is a legibility bump so
   math reads at the prose's full line height rather than just cap-height.

Both layouts call the same `encode(png, rows)` — there is no separate
`encode_inline` method. For Sixel, `rows` is currently ignored and the image
renders at its native pixel size. The threshold and proportional rows are both
computed at runtime from the same reference render, so they track RaTeX's actual
output rather than hard-coded pixel counts.

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
| `libc` v0.2 | `termbg` (unix only — `[target.'cfg(unix)']`): termios raw mode, `select(2)`, `ioctl(TIOCGWINSZ)`; `logging` (unix only) |
| `windows-sys` v0.59 | `termbg` (Windows only — `[target.'cfg(windows)']`): Console API for OSC 11, `GetConsoleScreenBufferInfo` for terminal width |

---

## Logging

- **Opt-in only.** No logging by default. Pass `--log <PATH>` to enable it.
  There is no default log path. Both `--log <PATH>` and `--log=<PATH>` are
  accepted; a missing/empty argument is a usage error (exit non-zero).
- **Never write to stdout.** stdout is reserved exclusively for the rendered
  feed written by `main`.
- Log file is opened for append at mode 0600 (unix). An exclusive writer lock
  is held for the process lifetime (unix: advisory `flock(LOCK_EX | LOCK_NB)`
  via `libc`; windows: `share_mode(FILE_SHARE_READ)` via std). A second
  instance pointed at the same path fails to acquire the lock, prints one
  warning to stderr, and continues running without logging. Read-only access
  (e.g. `tail -f`) is unaffected on both platforms.
- If the file cannot be opened, one warning goes to stderr and logging is
  disabled for the run.
- Level set via `LATERM_LOG_LEVEL` (debug, info, warn, error). Default: info.
  Applies only when logging is enabled.
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
It is kept for its behavioral specification value (kitty/imgcat escape
sequences, OSC 11 query mechanics, watch/tail logic) — read it when behavior
details for those areas are unclear. The Rust mathscan diverges from the
prototype: it echoes full transcripts rather than sparse anchor windows. Do not
modify the prototype when working on the Rust implementation. It is not part of
the Rust build.

---

## Prototype

`prototype/` contains the original Go implementation. It still builds with
`make build` from within that directory and can be run for behavioral
comparison. Its internal packages — especially `mathscan`, `kitty`, and
`imgcat` — are the reference for the corresponding Rust modules. The Go
implementation is not the shipped tool; `cargo build` in the repo root is the
authoritative build.
