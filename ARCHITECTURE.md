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
   conversation entries and its own stdin (manually pasted text);
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
   in the text flow. Both layouts call the same `encode(png, rows)`. Each emitted
   entry (one jsonl conversation entry, or one paste) is prefixed with a
   matched pair of bold, color-coded role markers — one per entry, not per line:
   opening `(u)> ` / closing `<(u)` (bold green) for user entries, `[a]> ` /
   `<[a]` (bold cyan) for assistant/agent entries, `{p}> ` / `<{p}` (bold
   magenta) for manually pasted text. The closing marker is appended inline at
   the end of the entry's last line (separated by a space) when content ends
   mid-line; when the entry ends in a tall/block image the closing marker falls to
   its own line. The entry's prose is rendered in the role's non-bold color (user:
   green `\x1b[32m`; assistant: cyan `\x1b[36m`; paste: magenta `\x1b[35m`) —
   the terminal carries the SGR color across its own soft-wraps, so the whole
   entry stays tinted at any scroll position with no wrapping logic in laterm.
   Bold markers are the primary role signal (and a colorblind backstop); the body
   tint is a secondary mid-entry orientation cue. Math images are not tinted —
   they render neutral per the contrast logic. The color is reset before the
   blank-line separator, so separators stay untinted. Markers use ANSI SGR escapes
   from the basic 8-color palette so they track the user's terminal color scheme
   rather than imposing fixed hues. Entries with no renderable content emit
   nothing (no marker). Each entry is followed by a single blank-line separator.
   stdout is written only by `main` and the output-feed modules (`main` emits
   the plain-color startup line and the optional missing-dir warning; `feed`
   emits the markers + tinted prose + images; `input` emits paste renders, the
   separator rule, and BEL); no other module writes stdout.

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

9. **Structured, leveled logging (opt-in).** No logging by default. Logging
   only happens when the user passes `--log <PATH>`. There is no default log
   path. When enabled, the file is opened for append at mode 0600 (unix) and
   an exclusive writer lock is held for the process lifetime (unix: advisory
   `flock(LOCK_EX | LOCK_NB)`; windows: `share_mode(FILE_SHARE_READ)`). A
   second instance pointed at the same path fails to acquire the lock, prints
   one warning to stderr, and continues running without logging; read-only
   access (e.g. `tail -f`) is unaffected. **Never writes to stdout.** Only
   fatal pre-exit messages go to stderr. Level is runtime-configurable via
   `LATERM_LOG_LEVEL` (debug/info/warn/error; default info) when logging is
   enabled.

---

## 2. Module Skeleton

### Crate Layout

```
src/
  main.rs          -- wiring: derive log dir, protocol select, watch loop, signal handling
  feed.rs          -- output/render orchestration: markers, tinted prose, math images, RenderCtx
  input.rs         -- manual paste-only stdin: PasteParser, separator rule, BEL
  watch.rs         -- polling tailer: *.jsonl, tail-only, partial-line buffering
  convo.rs         -- jsonl parser: extracts text from user/assistant entries
  mathscan.rs      -- LaTeX delimiter scanner: flat Vec<Segment>, document order
  render.rs        -- RaTeX pipeline: parse → layout → display list → PNG, theme
  graphics.rs      -- protocol selection + kitty/imgcat encoders (private fns)
  sixel.rs         -- Sixel encoder; supported() via DA1 query (parse_da1)
  termbg.rs        -- OSC 11 background query + shared query_terminal() helper
  logging.rs       -- log configuration, file + stderr output, level management
```

There are no `kitty.rs` / `imgcat.rs` files: the kitty graphics-protocol encoder
and the iTerm2 imgcat (OSC 1337) encoder are private functions inside
`graphics.rs`. `sixel` is the only encoder with its own module (it needs
`termbg` for the DA1/cell-size probes). `feed` and `input` are the output-feed
modules — together with `main` they are the only modules that write to stdout.

### Module Responsibilities and Constraints

**`main`**
- Responsibility (wiring only): Parse CLI flags (`-C`/`--cwd`, `--log`,
  `--catch-up`, `--help`/`--version`). If `-C`/`--cwd <PATH>` is given,
  `set_current_dir(PATH)` **before** deriving the log dir, so the existing
  `current_dir()`-based derivation produces the same canonical absolute path the
  OS gives Claude Code and the dir-name mangling matches by construction (no
  manual canonicalization). Initialize logging. Derive the Claude Code log
  directory for the (possibly changed) working directory. Print the startup line
  (see below). Select a graphics protocol via
  `graphics::select` **once**, exit immediately (clear error) if none of kitty,
  imgcat, or Sixel is supported, and wrap it in an `Arc` shared by catch-up, the
  watch loop, and the reader thread (so the sixel DA1 probe runs exactly once).
  Detect the terminal background via `termbg::query` and configure the renderer
  theme (`apply_theme`). Build a `feed::RenderCtx` once — its constructor renders
  the reference capital "X" (default 42 px if it fails) to derive the
  block-threshold (1.5× reference) and the proportional row sizing, and carries
  the protocol. Spawn the stdin reader thread (`input::read_input`, given the
  `RenderCtx` and the shared output mutex). If `--catch-up` was given, replay
  recent history (grouped by source entry) via `feed::emit_entry` before starting
  the tailer. Start the watch loop: for each line, `convo::extract` then
  `feed::emit_entry` under the output mutex. Handle SIGINT/SIGTERM to stop the
  watch loop cleanly.
- Log directory derivation: `~/.claude/projects/<cwd>` where every `/` in the
  absolute working directory path is replaced by `-`.
- Startup line: after the protocol is selected and the log dir is derived (and
  after any `-C` chdir), `main` writes ONE plain-color line to stdout (no SGR
  role color, no role marker): `laterm <version> monitoring <dir>/`, where
  `<version>` is `CARGO_PKG_VERSION` and `<dir>` is the derived watch dir
  tilde-collapsed (the home prefix — HOME on unix / USERPROFILE on windows, the
  same source `project_log_dir` uses — replaced by `~`; otherwise the absolute
  path), with a trailing `/`. The collapse is a pure helper (`display_dir`).
- Missing-dir warning: if the derived dir does not exist at startup, `main`
  writes a second plain-color stdout line right under the startup line phrased
  as not-yet (paste still works), e.g. `  no conversation log for this directory
  yet — paste to render, or start Claude Code here`. The watcher still waits for
  the dir; laterm does not exit.
- Must NOT contain: rendering, parsing, marker, or protocol logic. This is
  wiring only.

**`feed`**
- Responsibility: Turn conversation/paste text into the rendered stdout feed.
  Owns the `EntryStyle` role markers (`USER_STYLE`/`ASSISTANT_STYLE`/
  `PASTE_STYLE`), `role_style`, the reference-X sizing (`RenderCtx`,
  `ROW_SCALE`, `rows_for`), and the `emit_entry`/`emit_segments` pipeline.
  `emit_entry(style, texts, &RenderCtx)` walks each text's flat segment list:
  text echoed verbatim (newlines preserved), each math segment rendered via
  `render::render`, then `rows = max(1, round(height_px / reference_X_height_px ×
  1.25))` and `ctx.proto.encode(png, rows)`. Height ≥ 1.5× reference → block
  (newline + image + newline); otherwise inline. Each entry is bracketed by a
  matched bold, color-coded role-marker pair: opening `(u)> ` (bold green,
  `\x1b[1;32m`), `[a]> ` (bold cyan, `\x1b[1;36m`), `{p}> ` (bold magenta,
  `\x1b[1;35m`); closing (mirrored) `<(u)`/`<[a]`/`<{p}` in the same bold color.
  The closing marker is appended inline at the end of the last line (separated by
  a space) when content ends mid-line, or on its own line when the entry ends in
  a block image. Prose is rendered in the role's non-bold color
  (`\x1b[32m`/`\x1b[36m`/`\x1b[35m`), carried across the terminal's soft-wraps;
  math images render neutral; the close marker resets color before the blank-line
  separator. One marker-pair per entry — not per line. Entries with no renderable
  content emit nothing. `emit_entry` clears the manual-separator armed state;
  `arm_separator()` lets `input` re-arm/test it without a `feed → input` cycle.
  Writes stdout under the caller-held output mutex (no inner stdout lock — the
  mutex is the cross-thread serializer).
- Imports: `mathscan`, `render`, `graphics`, `logging`.
- Must NOT import: `input`, `watch`, `convo`, `termbg`.

**`input`**
- Responsibility: Manual paste-only stdin handling for the viewing window.
  Reads stdin in raw mode via `termbg::raw_input()` (echo OFF, canonical mode
  OFF, `ISIG` preserved). Enables bracketed paste (`ESC[?2004h`) at startup and
  disables it (`ESC[?2004l`) on exit. A byte-at-a-time `PasteParser` captures
  text between the bracketed-paste markers `ESC[200~` … `ESC[201~` and renders it
  via `feed::emit_entry` (`PASTE_STYLE`); multi-line pastes are one entry.
  Pressing Enter/Return writes a manual separator: a blank line, a terminal-width
  rule of `═` (U+2550, box-drawings double-horizontal) in bold yellow
  (`\x1b[1;33m`, width via `termbg::term_width()`, default 80), then a newline.
  Debounce via `feed::arm_separator()`: if the last output was already a
  separator, Enter beeps instead of stacking; any rendered content re-arms it.
  Typed printable keystrokes produce a throttled terminal BEL (`\x07`, ~once per
  250 ms); escape sequences and control bytes are consumed silently. Typed input
  is never rendered; Ctrl-C still fires SIGINT (`ISIG` preserved). All stdout
  writes (paste render, separator rule, BEL, bracketed-paste toggles) go under
  the shared output mutex so they don't interleave with the watch loop.
- Imports: `feed`, `termbg`, `logging`. `input → feed` (no cycle).

**`watch`**
- Responsibility: Poll `dir` at `interval` for `*.jsonl` files (`is_jsonl(path)`
  helper, also reused by catch-up). Tail-only semantics: existing files are
  recorded at their current size at startup; files appearing later start at
  offset 0. Per tick: read newly-appended bytes and split on `\n` with a
  cursor/index advance (not a per-line buffer rebuild — the old approach was
  O(N²) in buffer size), emitting complete lines as `Line { data: Vec<u8> }`
  (bytes only; the unused per-line path field was removed). Only the trailing
  partial line is copied once into the pending buffer. Handle truncation/rotation
  by resetting to offset 0. Wait gracefully if `dir` does not yet exist. Close
  the channel when the cancellation flag is signalled.
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
- Must NOT import: `watch`, `convo`, `mathscan`, `graphics`, `feed`, `input`.

**`graphics`**
- Responsibility: Select the terminal image protocol. Tries kitty first
  (env-based), then imgcat (env-based), then Sixel (DA1 tty round-trip). Returns
  a `Protocol` handle (carrying an `encode_fn`), or `None` when none is
  supported. The kitty and imgcat encoders are **private functions inside this
  module** (`kitty_supported`/`kitty_encode`, `imgcat_supported`/`imgcat_encode`),
  not separate files:
  - *kitty* — `kitty_supported` checks `KITTY_WINDOW_ID`, `TERM` containing
    `kitty`/`ghostty`, or `TERM_PROGRAM == "ghostty"`. `kitty_encode(png, rows)`
    transmits the PNG (`f=100`) in ≤4096-byte base64 chunks via `ESC _ G … ESC \`
    APC escapes, displayed at the cursor (`a=T,f=100,r=<rows>` on the first
    chunk) — the caller always supplies the proportional row count.
  - *imgcat* — `imgcat_supported` checks `TERM_PROGRAM == "iTerm.app"`,
    `LC_TERMINAL == "iTerm2"`, or `TERM_PROGRAM == "WezTerm"`.
    `imgcat_encode(png, rows)` emits
    `ESC ] 1337 ; File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len>:<base64> BEL`.
- Uses `base64` crate; imports `sixel`.
- Must NOT import: `render`, `mathscan`, `convo`, `watch`, `feed`, `input`.

**`imgcat` and `kitty`** — not standalone modules; see the private encoders in
`graphics` above.

**`sixel`**
- Responsibility: Detect Sixel support and encode PNG bytes as a Sixel escape
  sequence. `supported()` sends a DA1 query (`\x1b[c`) via `termbg`'s shared
  `query_terminal()` helper and returns true when attribute `4` appears in the
  `\x1b[?<attrs>c` reply (`parse_da1`). The helper handles raw-tty/Console API
  setup on both unix and Windows, so DA1 works on Windows Terminal.
  `set_background(r, g, b)` sets the opaque compositing color (call once at
  startup after detecting the terminal background). `detect_cell_height(timeout)`
  issues a **second** `termbg::query_terminal` (`\x1b[16t`, reply parsed by
  `parse_cell_height`) and stores the terminal's character-cell pixel height.
  `encode(png: &[u8], rows: u32)` decodes the PNG to RGBA8 using the `png` crate,
  composites any partially-transparent pixels against the background color (the
  Sixel encoder ignores alpha and would otherwise render those black), then pads
  the image height up to a multiple of the sixel band (`SIXEL_BAND` = 6 rows)
  and — when the cell height is known — to a multiple of the cell height (so
  Windows Terminal fills no extra dark cells; the last row is replicated). It
  then emits a standard Sixel stream: DCS introducer + 1:1 raster attributes, RGB
  color registers on a 0–`COLOR_SCALE` (100) scale, 6-row bands with per-color
  RLE (data chars based at `SIXEL_CHAR_BASE` = 0x3F). Colors are collected
  directly from the pixels (progressive bit-dropping when the palette would
  exceed 256 registers) — no quantization crate. The `rows` parameter is
  currently **ignored** — Sixel has no cell-based row scaling, so the image
  renders at its (cell-multiple-padded) pixel size (see section 5).
- Uses `png` crate (decode) and `termbg::query_terminal` (DA1 + cell-size
  probes). The Sixel encoder itself is in-house and dependency-free (no sixel or
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
  `raw_input() -> Option<RawInput>` opens a persistent raw-input mode for
  `main`'s read side (echo OFF, canonical mode OFF, `ISIG` preserved so Ctrl-C
  still fires SIGINT). RAII: the prior terminal mode is restored on drop.
  `term_width() -> usize` returns the current terminal width in columns, or 80
  when it cannot be determined. Unix: `ioctl(STDOUT_FILENO, TIOCGWINSZ)` via
  `libc`. Windows: `GetConsoleScreenBufferInfo` on the stdout handle via
  `windows-sys`. No new dependencies — both crates are already used by `termbg`.
  Every `unsafe` block carries a `// SAFETY:` rationale. Platform
  implementations:
  - **unix** — `query` opens `/dev/tty` and uses an RAII `RawModeGuard`
    (`cfmakeraw` on construct, prior termios restored on drop) so a panic in the
    I/O cannot strand the tty in raw mode; `select(2)` for the read timeout.
    `raw_input` operates on stdin (fd 0); clears `ECHO | ICANON | IEXTEN` in
    `c_lflag` (keeps `OPOST` so `\n`→`\r\n` output translation is unaffected);
    sets `VMIN=0 / VTIME=1`; a timed `read()` returns `Some(0)` on timeout (keep
    polling) and `None` on error/EOF; its own `Drop` restores the mode. Uses the
    `libc` crate.
  - **Windows** — operates on conin; clears `ENABLE_LINE_INPUT |
    ENABLE_ECHO_INPUT`, sets `ENABLE_VIRTUAL_TERMINAL_INPUT` so bracketed-paste
    VT sequences arrive; leaves output mode untouched; timed read via
    `WaitForSingleObject` / `ReadConsoleA`. The request bytes are written with
    `WriteConsoleA` to the **console output handle** (not `std::io::stdout`),
    matching the unix `/dev/tty` write so the stdout invariant holds. Handles are
    validated against `windows-sys`' `INVALID_HANDLE_VALUE`. Uses the
    `windows-sys` crate. Windows is not a stub — it has full OSC 11 parity.
- No dependencies on other laterm modules.

**`logging`**
- Responsibility: Configure structured, leveled logging. Logging is opt-in:
  path is resolved by `main` from `--log` only (no default). When a path is
  given, file output at mode 0600, append. Acquires an exclusive writer lock
  for the process lifetime (unix: advisory `flock(LOCK_EX | LOCK_NB)` via the
  existing `libc` dependency; windows: `OpenOptions::share_mode(FILE_SHARE_READ)`
  via std `OpenOptionsExt`, with `FILE_SHARE_READ` taken from `windows-sys`
  (feature `Win32_Storage_FileSystem`) rather than a hand-rolled `0x1` literal).
  A second instance that fails to acquire the lock prints one warning to stderr
  and disables logging for that instance; readers (e.g. `tail -f`) are
  unaffected. Parse and apply log level from `LATERM_LOG_LEVEL`. If the file
  cannot be opened (permission denied, etc.), prints one warning to stderr and
  disables logging for the run.
- Never writes to stdout.

### Dependency Direction

```
main
  |
  +---> feed ----> mathscan
  |          \---> render    (ratex-parser, ratex-layout, ratex-render, ratex-types)
  |          \---> graphics  (base64; kitty/imgcat encoders are private fns here)
  |          |          \--> sixel (png; calls termbg::query_terminal)
  |          \---> logging
  +---> input ---> feed
  |          \---> termbg
  +---> watch
  +---> convo
  +---> termbg
  +---> logging

(all modules may use logging)
```

Dependency direction is strictly downward. The only inter-feed edge is
`input → feed`; `feed` does not depend on `input`, so there is no cycle.
`watch`, `convo`, and `mathscan` depend only on the standard library; they do not
import any other laterm module. `feed` orchestrates `mathscan`/`render`/
`graphics`; `input` reads stdin via `termbg` and renders via `feed`. `termbg`
uses `libc` (unix) or `windows-sys` (Windows) for raw-mode terminal I/O. `render`
is the only module that imports the RaTeX crates. `main` uses the `ctrlc` crate
for cross-platform signal handling (SIGINT/SIGTERM) and selects a protocol via
`graphics::select` exactly once.

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
| `libc` v0.2 | `termbg` (unix only, `[target.'cfg(unix)']`); `logging` (unix only) | termios raw mode + `select(2)` for OSC 11 background query; `ioctl(TIOCGWINSZ)` for terminal width; `flock(LOCK_EX \| LOCK_NB)` for the exclusive log-file writer lock |
| `windows-sys` v0.59 | `termbg`, `logging` (Windows only, `[target.'cfg(windows)']`) | Console API (`GetStdHandle`, `SetConsoleMode`, `WaitForSingleObject`, `ReadConsoleA`, `WriteConsoleA`) for OSC 11 / DA1 / cell-size queries; `GetConsoleScreenBufferInfo` for terminal width; `INVALID_HANDLE_VALUE` for handle validation; `FILE_SHARE_READ` (feature `Win32_Storage_FileSystem`) for the log-file share mode |

No other external dependencies are permitted without updating this table and
providing justification.

---

## 3. Acceptance Criteria

Observable behavioral outcomes that must be true when implementation is
complete. Organized by component, in implementation priority order.

### Sidecar startup (Priority 1)

- `laterm` spawns no child process. Accepted flags: `-C`/`--cwd <PATH>`,
  `--log <PATH>`, `--catch-up[=<MINS>]` (bare = 5 minutes), `--help`/`-h`.
  Unknown flags or a missing/empty `--log`, `--cwd`, or `-C` argument exit
  non-zero with a usage message to stderr.
- `-C`/`--cwd <PATH>` sets the working directory used to derive the watched log
  dir, via `set_current_dir(PATH)` before dir derivation (footgun-free: the
  existing derivation then mangles the OS-canonical absolute path). A
  `set_current_dir` failure prints an error to stderr and exits non-zero.
- After protocol selection and dir derivation, `main` prints one plain-color
  stdout line `laterm <version> monitoring <dir>/` (`<dir>` tilde-collapsed
  under home, trailing `/`). If the derived dir does not exist, a second
  plain-color stdout line warns that there is no conversation log yet (paste
  still works); laterm keeps running.
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
- If the derived log directory does not yet exist, laterm prints the missing-dir
  warning line and waits (polling continues) rather than exiting.
- SIGINT and SIGTERM stop the poll loop and cause laterm to exit 0.
- Manual input: stdin is placed in raw mode via `termbg::raw_input()` (echo
  OFF, canonical mode OFF, `ISIG` preserved). Bracketed paste is enabled
  (`ESC[?2004h`) at startup and disabled (`ESC[?2004l`) on exit. Text pasted
  into the window is captured silently between the bracketed-paste markers and
  rendered through the same path as a conversation entry (full echo: prose
  verbatim, delimited math rendered in place). Each rendered entry — conversation
  or paste — is bracketed by a matched role marker pair (opening `(u)> ` / closing
  `<(u)` bold green, `[a]> ` / `<[a]` bold cyan, `{p}> ` / `<{p}` bold
  magenta); the closing marker is appended inline at the end of the last line, or
  on its own line when the entry ends with a block image. The entry's prose is
  tinted in the role's non-bold color; math images render neutral. Color is reset
  before the blank-line separator. One marker-pair per entry, basic-ANSI palette,
  followed by a single blank-line separator.
  Pressing Enter/Return writes a separator to stdout: a blank line, a
  terminal-width rule of `═` (U+2550) characters in bold yellow (width via
  `termbg::term_width()`, default 80), and a newline. If the last output was
  already a manual separator, Enter beeps instead (no stacked rules); any
  rendered content re-arms it. Typed printable keystrokes produce a throttled
  BEL (`\x07`, at most ~once per 250 ms); escape sequences and control bytes are
  consumed silently. There is no bare-expression rendering for undelimited typed
  input — that behavior is removed.

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

- Nothing appears on stdout except the plain-color startup line (and optional
  missing-dir warning) written by `main`, ANSI role markers (open+close pair,
  bold), role-tinted body text (non-bold), verbatim conversation text,
  graphics-protocol escape sequences (kitty, imgcat, or Sixel), blank-line entry
  separators, and the `═` separator rule.
- No logging unless `--log <PATH>` is given (no default path). When enabled:
  mode-0600 append, exclusive writer lock held for the process lifetime. A
  second instance pointed at the same path fails to acquire the lock, prints
  one warning to stderr, and continues without logging; readers (e.g. `tail -f`)
  are unaffected. If the file cannot be opened, one warning goes to stderr and
  logging is disabled for the run.
- Log level is configurable via `LATERM_LOG_LEVEL` (debug, info, warn, error)
  when logging is enabled.
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

[watch: poll ~250ms]
  |
  | one complete jsonl entry per line
  v

[convo::extract]
  |
  | text content from user/assistant entries
  | (None for other entry types, empty content, parse failure)
  v

[feed::emit_entry(style, texts, &RenderCtx)]   (under the shared output mutex)
  |
  | for each text: mathscan::scan ->
  |   Vec<Segment> — flat, document-order interleaving of Text and Math segments
  |   (newlines preserved; math-free text is included, not dropped)
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
                      block layout: newline + ctx.proto.encode(png, rows) + newline
                    : inline layout: ctx.proto.encode(png, rows) in flow
                  (ctx.proto selected ONCE in main: kitty if available, else
                   imgcat, else sixel; glyph color contrasts the detected
                   terminal background; sixel path uses opaque background and
                   pads to cell multiples; sixel ignores rows)

per entry (one marker-pair, not per line):
            bold open marker + role-tinted body text (non-bold) +
            rendered images in document order (images neutral) +
            bold close marker (inline, or own-line if image-final) +
            color reset + trailing blank line

catch-up: same path — each replayed jsonl entry's segments are grouped and
          emitted with a single feed::emit_entry call, so framing is
          byte-identical to the live tail (one marker-pair per entry).
manual paste (input thread): same feed::emit_entry path, PASTE_STYLE marker.
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

- **Polling, not inotify.** The watcher uses a ~250 ms polling interval. Math in
  a conversation entry may appear up to that long after it is written.

- **Sixel does not scale to terminal cell size.** kitty and imgcat use the
  proportional `rows` value to adapt image height to the terminal's cell
  dimensions. The Sixel encoder ignores `rows`: the image renders at its native
  pixel height, padded **up** to a whole number of sixel bands and (when the
  cell height was detected via `\x1b[16t`) a whole number of character cells, so
  Windows Terminal does not show extra dark cells. It does not scale the image
  *down/up* to a target row count. Proportional `rows` scaling for Sixel is a
  planned improvement.

---

## 6. Prototype

The original Go implementation lives under `prototype/`. It validated the
watch/tail logic, kitty/imgcat escape formats, and OSC 11 contrast detection —
those are carried forward in the Rust implementation. The Rust implementation
diverges from the prototype's sparse-feed/anchor-window mathscan model: it
echoes the full transcript verbatim instead of emitting only math-bearing lines.
The prototype still builds (see `prototype/Makefile`) and is kept for reference.
It is not part of the Rust build and is not the shipped tool.
