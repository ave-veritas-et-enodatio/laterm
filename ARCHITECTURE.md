# LaTerm Architecture Design

A Claude Code sidecar that watches the active conversation log for the current
project and renders LaTeX math expressions as inline images in a separate
graphics-capable terminal window (kitty graphics protocol, iTerm2 imgcat, or
Sixel).

This document is the authoritative design reference.

---

## 1. Invariants

These must hold regardless of implementation choices. Violating any invariant is a
blocking defect.

**Build and Deployment**

1. **Single self-contained binary, embedded fonts.** No external runtime
   dependencies. KaTeX fonts are embedded via RaTeX's `embed-fonts` feature.
   Binary name: `laterm`. Build with `cargo build --release`.

2. **Makefile wraps cargo as the canonical build entry point.** `make build`
   / `make test` / `make release` delegate to cargo. `make check` type-checks
   all three release targets (`aarch64-apple-darwin`, `x86_64-unknown-linux-gnu`,
   `x86_64-pc-windows-gnu`). `make dist` cross-builds all three into `dist/` via
   `cargo-zigbuild` (zig as the cross-linker), so all targets build from one
   host (e.g. a Mac); it uses the `-gnu` Windows triple because zigbuild cannot
   target `-msvc`. `make setup` installs the rustup targets and `cargo-zigbuild`
   (zig itself comes from the system package manager, e.g. `brew install zig`).
   `make fmt` / `make lint` run `cargo fmt` / `cargo clippy`. Plain `cargo build`
   / `cargo test` still work. `.github/workflows/release.yml` builds each target
   natively on its own OS runner on tags (so the shipped Windows binary is
   `-msvc`). No build outputs are placed in the source tree.

**Data Integrity**

3. **Read-only source.** laterm never writes to, modifies, or interferes with
   the Claude Code process or its log files. Its inputs are the `*.jsonl`
   conversation entries and its own stdin (manually typed/pasted expressions);
   it never writes back to either source.

4. **Tail-only, no replay.** At startup, each existing `*.jsonl` file's current
   byte size is recorded as its starting read offset. Bytes present at startup
   are never emitted. Files that appear after startup are read from byte 0.
   Exception: `--catch-up[=<MINS>]` replays entries whose RFC3339 `timestamp`
   falls within the last MINS minutes (default 5), across all `*.jsonl` files,
   in chronological order. The tailer records file offsets after catch-up
   completes, so caught-up entries are never re-emitted.

5. **Full transcript echo.** All conversation text is echoed verbatim to
   stdout — including messages and lines with no math. Every math expression is
   rendered at a proportional row count (`rows = max(1, round(png_height_px /
   reference_X_height_px × 1.25))`), where the reference is the pixel height of a
   rendered capital "X" measured once at startup and `1.25` (`ROW_SCALE`) is a
   legibility bump so math reads at the prose's full line height rather than just
   cap-height. A tall expression (rendered
   height ≥ 1.5× the reference X height) is a **block**: it appears on its own
   line (newline before + image + newline). A shorter expression stays **inline**
   in the text flow. Both layouts call the same `encode(png, rows)`. Only the
   `main` module writes to stdout.

**Resilience**

6. **Graphics-only, fail loud.** Math is rendered as images via a terminal
   graphics protocol. `graphics::select` tries kitty first (env-based), then
   imgcat (env-based), then Sixel (DA1 tty round-trip — probed last because it
   requires a terminal query). If the terminal supports none, laterm prints an
   error to stderr and exits at startup. There is no Unicode text fallback.

7. **Graceful rendering errors.** RaTeX returns `Result` on parse or render
   failure — it does not panic. Any error from the rendering pipeline results in
   the raw LaTeX being passed through as text (logged at debug level). The
   process must never crash due to a rendering error.

8. **Rendering resource caps.** Expressions are rendered at a size suitable for
   legibility (font size and DPI determined at runtime or by RaTeX's layout
   options). RaTeX is synchronous and returns `Result` — no wall-clock timeout
   is applied. A rendered PNG that exceeds 4096×4096 px (`MAX_WIDTH`/
   `MAX_HEIGHT` in `render.rs`) returns `ImageTooLarge` and the expression falls
   back to text passthrough.

   **Contrast.** At startup laterm queries the terminal background (OSC 11) and
   renders glyphs in a contrasting color (light on a dark terminal, dark on a
   light one) on a transparent background — no opaque box. If the query fails
   (unsupported or timeout) it falls back to black glyphs on an opaque white
   background, which is legible everywhere. The Sixel path is the exception: it
   renders on an opaque background filled with the detected terminal color (or
   white if unknown), since Sixel transparency is less universally honored.
   RaTeX exposes glyph color and background color/alpha via its layout and
   render options.

**Observability**

9. **Structured, leveled logging.** Always writes to a log file (keeps the
   rendered feed clean). Default path: `laterm.log` beside the executable,
   falling back to the current working directory. Override with `--log-file
   <PATH>`. File opened for append, mode 0600 (unix). **Never writes to
   stdout.** Only fatal pre-exit messages go to stderr. Level is
   runtime-configurable via `LATERM_LOG_LEVEL` (debug/info/warn/error; default
   info).

---

## 2. Module Skeleton

### Crate Layout

```
src/
  main.rs          -- wiring: derive log dir, protocol select, watch loop, signal handling
  watch.rs         -- polling tailer: *.jsonl, tail-only, partial-line buffering
  convo.rs         -- jsonl parser: extracts text from user/assistant entries
  mathscan.rs      -- LaTeX delimiter scanner: flat Vec<Segment>, document order
  render.rs        -- RaTeX pipeline: parse → layout → display list → PNG, theme
  graphics.rs      -- protocol selection: kitty (preferred), imgcat, or sixel
  kitty.rs         -- kitty graphics protocol encoder; supported() detection
  imgcat.rs        -- iTerm2 OSC 1337 encoder; supported() detection
  sixel.rs         -- Sixel encoder; supported() via DA1 query (parse_da1)
  termbg.rs        -- OSC 11 background query + shared query_terminal() helper
  logging.rs       -- log configuration, file + stderr output, level management
```

### Module Responsibilities and Constraints

**`main`**
- Responsibility: Parse CLI flags (`--log-file`, `--catch-up`, `--help`).
  Initialize logging. Derive the Claude Code log directory for the current
  working directory. Select a graphics protocol via `graphics::select` and exit
  immediately (with a clear error) if none of kitty, imgcat, or Sixel is
  supported.
  Detect the terminal background via `termbg::query` and configure the renderer
  theme. Render a reference capital "X" once (`render::render("X", false)`,
  default 42 px if it fails) to obtain the reference X height; this single
  value drives both the block-threshold (1.5× reference) and the proportional
  row count for every image. If `--catch-up` was given, replay recent history
  before starting the tailer. Start the watch loop. For each line, call
  `convo::extract`, then `mathscan::scan`; walk the returned flat segment list
  in order — text segments echoed verbatim, each math segment rendered via
  `render::render`, then `rows = max(1, round(height_px / reference_X_height_px × 1.25))`
  computed, and the image emitted via `proto.encode(png, rows)`. Height ≥
  1.5× reference → block layout (newline + image + newline); otherwise inline
  (in flow). Handle SIGINT/SIGTERM to stop the watch loop cleanly.
- Manual input: also reads stdin (line mode) and renders each typed or pasted
  line through the same path; a line with no delimited math is treated as one
  bare LaTeX expression. A shared output mutex keeps conversation and manual
  renders from interleaving mid-image.
- Log directory derivation: `~/.claude/projects/<cwd>` where every `/` in the
  absolute working directory path is replaced by `-`.
- Must NOT contain: rendering logic, parsing logic, or sanitization logic.
  This is wiring only. Only this module writes to stdout.

**`watch`**
- Responsibility: Poll `dir` at `interval` for `*.jsonl` files. Tail-only
  semantics: existing files are recorded at their current size at startup;
  files appearing later start at offset 0. Per tick: read newly-appended bytes,
  split on `\n`, emit complete lines on the channel. Buffer partial lines.
  Handle truncation/rotation by resetting to offset 0. Wait gracefully if `dir`
  does not yet exist. Close the channel when the context/cancellation token is
  signalled.
- No dependencies on other laterm modules.

**`convo`**
- Responsibility: Parse one complete jsonl line. Extract text content from
  `message.content` for entries whose top-level `type` is `"user"` or
  `"assistant"`. Content may be a JSON array of blocks (return `type:"text"`
  blocks only) or a plain JSON string. Return empty/None for any other entry
  type, empty content, or parse failure. Never panic.
- Uses `serde_json` for parsing.
- No dependencies on other laterm modules.

**`mathscan`**
- Responsibility: Scan a markdown string for LaTeX math and return a flat,
  ordered stream of all text and math in the input. Return type:
  `Vec<Segment>` where each `Segment` is either `Text(String)` or
  `Math { expr: String, display: bool }`, in document order, with newlines
  preserved. Recognized delimiters: `$$...$$` and `\[...\]` (display=true);
  `$...$` and `\(...\)` (display=false). `\$` is a literal dollar. `\\`
  prevents the following `$` from opening math. Unterminated and empty spans
  are treated as literal text. Multi-line display blocks are supported.
  Text on lines with no math is emitted as `Text` segments unchanged — nothing
  is dropped. No trimming, no anchor windowing, no neighbor-context logic.
  Returns an empty vec only for empty input.
- No dependencies on other laterm modules.

**`render`**
- Responsibility: Run the full LaTeX→PNG pipeline using RaTeX. Call
  `ratex_parser::parse`, then `ratex_layout::layout` and `to_display_list`,
  then `ratex_render::render_to_png`. RaTeX is synchronous and returns `Result`
  — no timeout is applied. Reject PNGs exceeding 4096×4096 px
  (`ImageTooLarge`). On any error return `Err`; the caller passes the raw
  LaTeX through as text. Glyph color is set via
  `LayoutOptions::with_color(Color)`; background color and alpha via
  `RenderOptions.background_color` (`Color { r, g, b, a: f32 }`; `a = 0.0` is
  transparent). Returns the PNG bytes and the rendered image height in pixels
  (used by the caller to choose inline vs block layout).
- Imports: `ratex-parser`, `ratex-layout`, `ratex-render`, `ratex-types`.
- Must NOT import: `watch`, `convo`, `mathscan`, `graphics`, `kitty`, `imgcat`.

**`graphics`**
- Responsibility: Select the terminal image protocol. Tries kitty first
  (env-based), then imgcat (env-based), then Sixel (DA1 tty round-trip). Returns
  a protocol handle, or `None` when none is supported.
- Imports: `kitty`, `imgcat`, `sixel`.
- Must NOT import: `render`, `mathscan`, `convo`, `watch`.

**`kitty`**
- Responsibility: Detect kitty graphics support and encode PNG bytes as the
  kitty graphics protocol. PNG (`f=100`) transmitted in ≤4096-byte base64
  chunks via `ESC _ G ... ESC \` APC escapes, displayed at the cursor (`a=T`).
  `supported()` checks `KITTY_WINDOW_ID`, `TERM` containing `kitty`/`ghostty`,
  or `TERM_PROGRAM == "ghostty"`. `encode(png: &[u8], rows: u32)`: emits
  `a=T,f=100,r=<rows>` — the caller always supplies the proportional row count.
- Uses `base64` crate.
- No dependencies on other laterm modules.

**`imgcat`**
- Responsibility: Detect iTerm2/WezTerm imgcat support and encode PNG bytes as
  the iTerm2 OSC 1337 inline-image escape sequence.
  `supported()` checks `TERM_PROGRAM == "iTerm.app"`, `LC_TERMINAL == "iTerm2"`,
  or `TERM_PROGRAM == "WezTerm"`. `encode(png: &[u8], rows: u32)`: emits
  `ESC ] 1337 ; File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len>:<base64> BEL`,
  where `rows` is in terminal cells (the proportional row count from `main`).
- Uses `base64` crate.
- No dependencies on other laterm modules.

**`sixel`**
- Responsibility: Detect Sixel support and encode PNG bytes as a Sixel escape
  sequence. `supported()` sends a DA1 query (`\x1b[c`) via `termbg`'s shared
  `query_terminal()` helper and returns true when attribute `4` appears in the
  `\x1b[?<attrs>c` reply (`parse_da1`). The helper handles raw-tty/Console API
  setup on both unix and Windows, so DA1 works on Windows Terminal.
  `encode(png: &[u8], rows: u32)` decodes the PNG to RGBA8 using the `png`
  crate, then emits a standard Sixel stream: DCS introducer + 1:1 raster
  attributes, RGB color registers scaled to 0–100, 6-row bands with per-color
  RLE. Colors are collected directly from the pixels (with progressive
  bit-dropping when the palette would exceed 256 registers) — no quantization
  crate is used. The `rows` parameter is currently **ignored** — Sixel has no
  cell-based row scaling, so the image renders at its native pixel size (known
  limitation; see section 5). The renderer uses an **opaque** background
  (terminal color or white) for the Sixel path, because Sixel transparency is
  less universally honored than the transparent path kitty/imgcat use.
- Uses `png` crate (decode) and `termbg::query_terminal` (DA1 probe).
  The Sixel encoder itself is in-house and dependency-free (no sixel or
  quantization crate).
- No laterm module imports except `termbg::query_terminal`.

**`termbg`**
- Responsibility: Query the terminal background color via OSC 11
  (`ESC ] 11 ; ?`), with a timeout, so the renderer can choose a contrasting
  glyph color. Parses the `rgb:RRRR/GGGG/BBBB` reply.
  `query(timeout) -> Option<(u8, u8, u8)>` and `is_dark(r, g, b) -> bool`
  are platform-independent. `query_terminal(request, timeout) -> Option<String>`
  is a shared helper used by both the OSC 11 query and `sixel`'s DA1 probe —
  it sends an arbitrary terminal request in raw mode and returns the reply.
  Platform implementations:
  - **unix** — opens `/dev/tty`, uses the `libc` crate for termios raw mode
    (`cfmakeraw`/`tcsetattr`) and `select(2)` for the read timeout.
  - **Windows** — uses the `windows-sys` crate's Console API:
    `GetStdHandle`, `SetConsoleMode` with `ENABLE_VIRTUAL_TERMINAL_INPUT`,
    `WaitForSingleObject` for the timeout, `ReadConsoleA` for the reply.
    Windows is not a stub — it has full OSC 11 parity.
- No dependencies on other laterm modules.

**`logging`**
- Responsibility: Configure structured, leveled logging. File output at mode
  0600, append, path resolved by `main` (from `--log-file` or the default
  beside the executable). Parse and apply log level from `LATERM_LOG_LEVEL`.
  If the file cannot be opened, prints one warning to stderr and disables
  logging for the run.
- Never writes to stdout.

### Dependency Direction

```
main
  |
  +---> watch
  +---> convo
  +---> mathscan
  +---> render  (uses ratex-parser, ratex-layout, ratex-render, ratex-types)
  +---> graphics --> kitty  (uses base64)
  |             |--> imgcat (uses base64)
  |             \--> sixel  (uses png; calls termbg::query_terminal)
  +---> termbg
  +---> logging

(all modules may use logging)
```

Dependency direction is strictly downward. No cycles. `watch`, `convo`, and
`mathscan` depend only on the standard library; they do not import any other
laterm module. `termbg` uses `libc` (unix) or `windows-sys` (Windows) for
raw-mode terminal I/O. `render` is the only module that imports the RaTeX
crates. `main` uses the `ctrlc` crate for cross-platform signal handling
(SIGINT/SIGTERM). `main` selects a protocol via `graphics::select`.

### Dependency Justification

| Dependency | Module | Justification |
|---|---|---|
| `ratex-parser` v0.1.9 | `render` | KaTeX-compatible LaTeX parser; pure Rust, no external runtime |
| `ratex-layout` v0.1.9 | `render` | TeX box-model layout engine for the RaTeX pipeline |
| `ratex-render` v0.1.9 (`embed-fonts`) | `render` | PNG renderer; `embed-fonts` bundles KaTeX fonts into the binary, eliminating external font dependencies |
| `ratex-types` v0.1.9 | `render` | Shared type definitions required to wire the RaTeX pipeline stages |
| `serde` v1 | `convo` | Derive macros for JSON deserialization |
| `serde_json` v1 | `convo` | Parsing `.jsonl` conversation log entries |
| `base64` v0.22 | `graphics` | Encoding PNG bytes for kitty and imgcat image protocols |
| `png` v0.17 | `sixel` | PNG→RGBA8 decode for the Sixel encoder; no sixel or quantization crate is used — the encoder is in-house |
| `chrono` v0.4 | `main` | RFC3339 timestamp parsing for `--catch-up` window filtering |
| `ctrlc` v3 | `main` | Cross-platform SIGINT/SIGTERM handler (MIT/Apache-2.0) |
| `libc` v0.2 | `termbg` (unix only, `[target.'cfg(unix)']`) | termios raw mode + `select(2)` for OSC 11 background query |
| `windows-sys` v0.59 | `termbg` (Windows only, `[target.'cfg(windows)']`) | Console API (`GetStdHandle`, `SetConsoleMode`, `WaitForSingleObject`, `ReadConsoleA`) for OSC 11 background query |

No other external dependencies are permitted without updating this table and
providing justification.

---

## 3. Acceptance Criteria

Observable behavioral outcomes that must be true when implementation is
complete. Organized by component, in implementation priority order.

### Sidecar startup (Priority 1)

- `laterm` spawns no child process. Accepted flags: `--log-file <PATH>`,
  `--catch-up[=<MINS>]` (bare = 5 minutes), `--help`/`-h`. Unknown flags or a
  missing `--log-file` argument exit non-zero with a usage message to stderr.
- If the terminal supports none of kitty, imgcat, or Sixel (`graphics::select`
  returns `None`), laterm prints a clear error to stderr and exits non-zero
  immediately. No further work is done.
- When multiple protocols are available, kitty is preferred over imgcat over
  Sixel.
- At startup the terminal background is queried (OSC 11); on success glyphs are
  rendered in a contrasting color on a transparent background, otherwise
  black-on-white is used. Detection failure is not fatal.
- If `std::env::current_dir()` fails, laterm prints an error to stderr and
  exits non-zero.
- If the derived log directory does not yet exist, laterm waits (polling
  continues) rather than exiting.
- SIGINT and SIGTERM stop the poll loop and cause laterm to exit 0.
- Manual input: a line typed or pasted into the window is rendered. A line with
  delimited math is treated like conversation text; a line with no delimiters is
  rendered as one bare LaTeX expression. Empty lines are ignored.

### File watcher (Priority 1)

- Bytes present in `*.jsonl` files at startup are not emitted.
- New bytes appended after startup are emitted as complete `\n`-terminated lines.
- A file appearing after startup is read from byte 0.
- A file that shrinks (truncation/rotation) is re-read from byte 0.
- A partial line (no trailing `\n`) is buffered and emitted once completed.
- The channel/iterator is closed when cancellation is signalled.

### Conversation parser (Priority 2)

- Entries with `type` other than `"user"` or `"assistant"` return None/empty.
- `message.content` as a JSON array: only `type:"text"` blocks are returned.
- `message.content` as a plain JSON string: returned as a single segment.
- Malformed JSON returns None/empty without panicking.

### Math scanner (Priority 2)

- `$\sigma$` → a `Text` segment for any surrounding text plus one inline
  `Math` segment `"\sigma"`, in document order.
- `$$\int_0^1 f(x)\,dx$$` → a display `Math` segment; multi-line spans
  supported; trailing text on the closing line is emitted as a `Text` segment
  immediately after.
- `\(\sigma\)` → inline math; `\[...\]` → display math.
- `\$` is treated as a literal dollar and does not open a math span.
- Empty expressions (e.g. `$$`) and unterminated spans are treated as literal
  text and emitted as `Text` segments.
- Two expressions on one line appear as two `Math` segments in document order,
  interleaved with the text between them — no duplication.
- Text on lines with no math is emitted verbatim as `Text` segments; no line
  or segment is dropped. `scan` returns an empty vec only for empty input.
- Newlines in the input are preserved in `Text` segments — the output is a
  faithful interleaved representation of the full input.

### Renderer (Priority 3)

- `render::render` returns `Err` on RaTeX parse or render failure; the process
  does not exit.
- A PNG exceeding 4096×4096 px returns `ImageTooLarge`.
- `render::render` returns the rendered image height in pixels. `main` renders
  a reference capital "X" once at startup (default 42 px if it fails). This
  single reference value drives two things: (1) expressions whose height is
  ≥ 1.5× the reference are laid out as **block** (own line); shorter expressions
  are laid out **inline** (in flow). (2) For every image, `rows = max(1,
  round(png_height_px / reference_X_height_px × 1.25))` is passed to
  `encode(png, rows)`, so displayed height scales proportionally to terminal text
  size (the `1.25` `ROW_SCALE` is a legibility bump).
- On any error from `render::render`, `main` logs at debug and passes the raw
  LaTeX through (delimited) rather than dropping it.

### Graphics protocols (Priority 3)

- `graphics::select` returns kitty when `kitty::supported()` (env:
  `KITTY_WINDOW_ID`, `TERM` containing `kitty`/`ghostty`,
  `TERM_PROGRAM == "ghostty"`), else imgcat when `imgcat::supported()`
  (`TERM_PROGRAM == "iTerm.app"`, `LC_TERMINAL == "iTerm2"`,
  `TERM_PROGRAM == "WezTerm"`), else Sixel when `sixel::supported()` (DA1
  reply contains attribute `4`), else `None`.
- `imgcat::encode(png, rows)` emits `\x1b]1337;File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len>:<base64>\a` (BEL), where `rows` is the proportional terminal-cell row count.
- `kitty::encode(png, rows)` emits one or more `\x1b_G…\x1b\\` APC escapes
  (first with `a=T,f=100,r=<rows>`), base64 PNG in ≤4096-byte chunks, final
  chunk `m=0`. `rows` is always present — it is the proportional row count.
- `termbg::query` parses an OSC 11 reply (`rgb:RRRR/GGGG/BBBB`) to RGB;
  `is_dark` thresholds on relative luminance.

### Cross-cutting

- Nothing appears on stdout except verbatim conversation text and
  graphics-protocol escape sequences (kitty, imgcat, or Sixel).
- Log output always goes to a file (mode 0600, append). Default path:
  `laterm.log` beside the executable (falls back to cwd). Overridden by
  `--log-file <PATH>`. If the file cannot be opened, one warning goes to stderr
  and logging is disabled for the run.
- Log level is configurable via `LATERM_LOG_LEVEL` (debug, info, warn, error).
- `--catch-up[=<MINS>]`: before the tail starts, all `*.jsonl` files in the
  log directory are scanned; entries whose RFC3339 `timestamp` is at or after
  `now - MINS minutes` are collected, sorted by timestamp, and rendered in
  order. The tailer then records file offsets, so no caught-up entry is
  re-emitted. Absent or unparseable timestamps are excluded.
- `make release` (or `cargo build --release`) produces a self-contained binary
  with fonts embedded. `make dist` builds release binaries for all three
  release targets into `dist/`.
- `make test` (or `cargo test`) runs unit tests.

---

## 4. Data Flow Summary

```
Claude Code process
  |
  | appends entries to ~/.claude/projects/<cwd>/.../*.jsonl
  v

[watch: poll ~500ms]
  |
  | one complete jsonl entry per line
  v

[convo::extract]
  |
  | text content from user/assistant entries
  | (None for other entry types, empty content, parse failure)
  v

[mathscan::scan]
  |
  | Vec<Segment> — flat, document-order interleaving of Text and Math segments
  | (newlines preserved; math-free text is included, not dropped)
  v

walk flat segment list in order:
  Text segment  -> write text verbatim (newlines included)
  Math segment  -> [render::render] -> Result<(png_bytes, height), _>
                     |
                     +-- ratex_parser::parse
                     +-- ratex_layout::layout + to_display_list
                     +-- ratex_render::render_to_png
                     |     size cap: 4096×4096 px → ImageTooLarge
                     |     on Err: pass through raw LaTeX (delimited)
                     v
                  rows = max(1, round(height / reference_X_height × 1.25))
                  height >= 1.5× reference_X_height ?
                      block layout: newline + proto.encode(png, rows) + newline
                    : inline layout: proto.encode(png, rows) in flow
                  (proto = kitty if available, else imgcat, else sixel;
                   glyph color contrasts the detected terminal background;
                   sixel path uses opaque background; sixel ignores rows — native size)

stdout: full conversation text + rendered images in document order
```

---

## 5. Known Limitations

- **Graphics-protocol terminals only.** laterm requires a terminal that
  supports the kitty graphics protocol (kitty, ghostty), the iTerm2 imgcat
  protocol (iTerm2, WezTerm), or Sixel (Windows Terminal v1.22+, xterm, foot,
  mlterm, WezTerm, and others). Selection order: kitty → imgcat → Sixel.
  There is no Unicode text fallback; unsupported terminals are rejected at
  startup.

- **Background detection is best-effort.** Glyph contrast relies on an OSC 11
  background query; terminals that do not answer (within 200 ms) get the
  black-on-white fallback rather than theme-matched glyphs.

- **Polling, not inotify.** The watcher uses a 500 ms polling interval. Math in
  a conversation entry may appear up to 500 ms after it is written.

- **Sixel does not scale to terminal cell size.** kitty and imgcat use the
  proportional `rows` value to adapt image height to the terminal's cell
  dimensions. The Sixel encoder ignores `rows` and renders at native pixel
  size. Sixel cell-based scaling is a planned improvement.

---

## 6. Prototype

The original Go implementation lives under `prototype/`. It validated the
watch/tail logic, kitty/imgcat escape formats, and OSC 11 contrast detection —
those are carried forward in the Rust implementation. The Rust implementation
diverges from the prototype's sparse-feed/anchor-window mathscan model: it
echoes the full transcript verbatim instead of emitting only math-bearing lines.
The prototype still builds (see `prototype/Makefile`) and is kept for reference.
It is not part of the Rust build and is not the shipped tool.
