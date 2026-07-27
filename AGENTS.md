# AGENTS.md — LaTerm

Guidance for agents (automated or human) working on this codebase.
**Read ARCHITECTURE.md first.** It is the authoritative design reference and
supersedes any inference you draw from code alone.

---

## Project Overview

LaTerm is a Claude Code sidecar renderer written in Rust. It receives the
active conversation's turns — pushed live by Claude Code hooks over a
Unix-domain socket — and renders LaTeX math expressions as inline images in a
separate graphics-capable terminal window (kitty graphics protocol, iTerm2
imgcat, or Sixel).

Run it from the project directory in a separate kitty, ghostty, iTerm2, WezTerm,
Windows Terminal, or any Sixel-capable terminal window:

```sh
cargo run --release
# or, if installed on PATH:
laterm [-C <PATH>] [--log <PATH>] [--catch-up[=<MINS>]] [--version] [--help]
laterm --install-hooks [--project|--project-local|--global]   # one-time: configure Claude Code
laterm --hook <UserPromptSubmit|Stop>          # invoked BY Claude Code, not by you
```

It spawns no child process. Claude Code, once configured via `--install-hooks`,
invokes `laterm --hook <event>` on each turn; that thin forwarder pushes the
turn text over a Unix-domain socket to the running renderer, which echoes the
full conversation text to stdout — bracketed by color-coded role markers with
the body tinted by role — with all LaTeX math expressions rendered as inline
images in place. Live tailing of the transcript is gone (recent Claude Code
writes it asynchronously, so a tailer lags the live turn); hooks replace it.
Pass `--catch-up` to first replay recent history from the completed transcript
before the live path begins. You can also paste text directly into its window to
render it on the spot.

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
make install  # copy this OS's dist binary to ~/bin (INSTALL_DIR overrides);
              #   rm-then-cp for a fresh inode + dequarantine on macOS
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
  main.rs      CLI entry point. Wiring only. No child process. Modes: normal
               renderer / --hook forwarder / --install-hooks.
  feed.rs      Output/render orchestration: role markers, tinted prose, math
               images. RenderCtx (reference-X sizing + protocol). Writes stdout.
  input.rs     Manual paste-only stdin: PasteParser, separator rule, BEL.
               Writes stdout (paste renders via feed::emit_entry).
  ipc.rs       UDS transport: socket-path derivation, listener accept-loop,
               --hook forward client. Unix now; Windows named-pipe later.
  hook.rs      Pure hook-payload parser. UserPromptSubmit/Stop JSON -> (role,
               text) + cwd. No laterm imports (mirrors convo's old purity).
  convo.rs     Historical jsonl parser (catch-up ONLY). Text from user/assistant
               entries; owns is_jsonl(). Not on the live path.
  mathscan.rs  LaTeX delimiter scanner. Returns flat Vec<Segment>, document order.
  render.rs    PNG renderer. RaTeX parse→layout→display list→PNG pipeline.
  graphics.rs  Protocol selection + kitty/imgcat encoders (private fns).
  sixel.rs     Sixel encoder. supported() via DA1 query (parse_da1).
  termbg.rs    OSC 11 background query + shared query_terminal() helper.
  logging.rs   Log configuration, file + stderr output, level management.
```

There are no separate `kitty.rs` / `imgcat.rs` files: the kitty graphics-protocol
encoder and the iTerm2 imgcat (OSC 1337) encoder are private functions inside
`graphics.rs` (`kitty_supported`/`kitty_encode`, `imgcat_supported`/
`imgcat_encode`). Only `sixel` is a standalone module (it needs `termbg` for the
DA1 probe and is larger).

### Module responsibilities in brief

**`main`** — Wiring only. Three modes, dispatched from CLI flags before any
other work:

1. **`--hook <event>`** (forward-and-exit) — a thin forwarder. Read the hook
   JSON from stdin, derive the socket path from the payload's `cwd` field (the
   authoritative project dir — `hook::payload_cwd` minimally parses just `cwd`,
   then `ipc::socket_path(cwd)`; NOT the hook process's own working directory, so
   client and renderer rendezvous regardless of where the hook is invoked), write
   the raw bytes over the socket, exit 0. NO protocol select, background detection,
   rendering, theme, or paste thread. Writes NOTHING to stdout (a
   `UserPromptSubmit` hook's stdout is injected into the model's context). If no
   listener is present (socket missing/refused) exit 0 SILENTLY — never block or
   fail Claude Code.
2. **`--install-hooks [--project|--project-local|--global]`** (configure-and-exit) — merge the
   two hook entries into the target `settings.json` (`--project` →
   `.claude/settings.json` (shared/committed); `--project-local` →
   `.claude/settings.local.json` (personal/untracked); `--global` →
   `~/.claude/settings.json`; default `--project-local` — never touch shared
   settings unless directed; at most one target flag, more is a usage error)
   without clobbering existing settings
   (a `permissions` block survives), idempotently, then exit. Each entry invokes
   `<current_exe()-abs-path> --hook <event>` so it fires regardless of PATH.
3. **normal (the renderer)** — Parse CLI flags (`-C`/`--cwd`, `--log`,
   `--catch-up`, `--help`/`--version`). If `-C`/`--cwd <PATH>` is given,
   `set_current_dir(PATH)` **before** deriving the log dir and socket path
   (footgun-free: the `current_dir()` derivation then mangles the OS-canonical
   absolute path, so both match by construction). Initialize logging. Derive the
   Claude Code log directory (`~/.claude/projects/<cwd-with-slashes-as-dashes>`)
   and the socket path (via `ipc`, sharing the same dash-mangling helper). Write
   the plain-color startup line `laterm <version> monitoring <dir>/` (`<dir>`
   tilde-collapsed via `display_dir`) — and, when the derived dir does not exist,
   a second plain-color line warning there is no conversation log yet (paste
   still works); laterm keeps running (the listener accepts hooks; only
   `--catch-up` needs the dir). Select a graphics protocol via `graphics::select`
   **once** and exit with an error if none of kitty, imgcat, or Sixel is
   supported; the single `Arc<graphics::Protocol>` is shared by catch-up, the
   listener loop, and the reader thread (so the sixel DA1 probe runs once).
   Detect the terminal background (`termbg::query`) and configure the theme
   (`apply_theme`). Build a `feed::RenderCtx` once. Spawn the stdin reader thread
   (`input::read_input`). If `--catch-up` was given, replay recent history from
   the completed transcript (all `*.jsonl`, chronological, grouped by source
   entry) via `convo::extract` + `feed::emit_entry` before starting the listener
   — one marker-pair per entry, byte-identical to the live path. Start the `ipc`
   listener loop: for each received payload, `hook::parse` it, drop it if its
   `cwd` does not match the watched cwd, map the parsed role to `feed`'s
   EntryStyle (user → `USER_STYLE`, assistant → `ASSISTANT_STYLE`), and
   `feed::emit_entry` (under the output mutex). Handle SIGINT/SIGTERM (stop the
   listener, remove the socket file, exit 0).

Contains no rendering, parsing, transport, protocol, or marker logic — parsing
lives in `hook`/`convo`, transport in `ipc`, rendering/markers in `feed`/`input`.

**`feed`** — Output/render orchestration. Owns the `EntryStyle` role markers
(`USER_STYLE`/`ASSISTANT_STYLE`/`PASTE_STYLE`), `role_style`, the
reference-X sizing (`RenderCtx`, `ROW_SCALE`, `rows_for`, reference render,
default 42 px), and the `emit_entry`/`emit_segments` pipeline. `emit_entry`
takes a `&RenderCtx` (collapsing the former block-threshold/ref-height/proto
trio). Walks each text's flat segment list in order: text segments echoed
verbatim (newlines preserved), math segments rendered via `render::render`, then
`rows = max(1, round(height_px / reference_X_height_px × 1.25))` and
`ctx.proto.encode(png, rows)`. Height ≥ 1.5× reference → block (newline + image +
newline); otherwise inline. Each entry is bracketed by a matched pair of bold,
color-coded role markers: opening `(u)> ` (bold green, `\x1b[1;32m`), `[a]> `
(bold cyan, `\x1b[1;36m`), `{p}> ` (bold magenta, `\x1b[1;35m`); closing
(mirrored) `<(u)`/`<[a]`/`<{p}` in the same bold color. The close marker is
appended inline at the end of the last line (separated by a space) when content
ends mid-line, or on its own line when the entry ends in a block image. Prose is
rendered in the role's non-bold color (`\x1b[32m`/`\x1b[36m`/`\x1b[35m`), carried
across the terminal's soft-wraps; math images render neutral; color is reset by
the close marker before the blank-line separator. One marker-pair per entry.
Entries with no renderable content emit nothing. `emit_entry` clears the
manual-separator armed state; `arm_separator()` lets `input` re-arm/test it
without a cycle. Writes stdout under the caller-held output mutex (no inner
stdout lock). Imports `mathscan`, `render`, `graphics`, `logging`.

**`input`** — Manual paste-only stdin handling. Reads stdin in raw mode via
`termbg::raw_input()` (echo OFF, canonical mode OFF, `ISIG` preserved). Enables
bracketed paste (`ESC[?2004h`) at startup and disables it (`ESC[?2004l`) on exit.
The `PasteParser` byte-state machine captures text between the bracketed-paste
markers `ESC[200~` … `ESC[201~` and renders it via `feed::emit_entry`
(`PASTE_STYLE`). Pressing Enter/Return writes a manual separator: a blank line,
a terminal-width rule of `═` (U+2550) in bold yellow (`\x1b[1;33m`, width from
`termbg::term_width()`, default 80), then a newline. Debounce via
`feed::arm_separator()`: if the last output was already a separator, Enter beeps
instead of stacking; any rendered content re-arms it. Typed printable keystrokes
produce a throttled BEL (`\x07`, ~once per 250 ms); escape sequences and control
bytes are consumed silently. Typed input is never rendered. All stdout writes
(paste render, separator rule, BEL, bracketed-paste toggles) go under the shared
output mutex so they don't interleave with the listener loop. Imports `feed`,
`termbg`, `logging`. Dependency direction is `input → feed` (no cycle).

**`ipc`** — The Unix-domain-socket transport. Three pieces: (1) `socket_path()`
— deterministic derivation shared by the listener and the `--hook` client, so
they rendezvous: under `$XDG_RUNTIME_DIR` if set else `~/.cache/laterm/`,
filename = the SAME dash-mangled cwd string `main` uses for the log dir (one
shared helper, not a copy) plus `.sock`; creates the parent dir as needed. (2)
The **listener** (replaces `watch`'s producer role): binds a `UnixListener`
(removing a stale socket file first), accepts connections one at a time in
arrival order, reads each to EOF (one raw payload per connection), hands the
payload to the main loop; observes the cancellation flag and removes the socket
file on shutdown. (3) The **`--hook` client**: `send`/`forward` connect to the
socket derived from the payload's `cwd` (parsed by `hook`, sequenced by `main` —
`ipc` does no JSON parsing and imports no laterm module) and write the raw stdin
bytes, exit; on any connect/write failure return quietly so `main` exits 0;
write NOTHING to stdout ever. Platform-split like `termbg`:
`std::os::unix::net` now, Windows named-pipe later. No laterm module imports; no
new external crate.

**`hook`** — A PURE payload parser (mirrors `convo`'s old purity: no laterm
imports, `serde`/`serde_json`, never panics). Parses a hook stdin JSON payload
into the same neutral `(role, text)` shape `convo::extract` returns (role is a
parser enum, NOT `feed`'s `EntryStyle` — `main` does that mapping, keeping this
module import-free): `UserPromptSubmit` → `(user, prompt)`; `Stop` → `(assistant,
last_assistant_message)` (full, untruncated). Also exposes the payload `cwd` so
`main` can drop turns from other projects. Returns None/empty for an unrecognized
`hook_event_name`, empty content, a `cwd` mismatch, or parse failure.

**`convo`** (catch-up historical path ONLY) — Parses one jsonl line from a
COMPLETED transcript. This is the only surviving jsonl-parse path (live is
hooks). Returns text content from `"user"` and `"assistant"` entries only.
`message.content` may be a JSON array of blocks (returns `type:"text"` blocks) or
a plain JSON string (returned as a single item). Returns None/empty for all other
cases, including parse failure. Never panics. Owns the `is_jsonl(path)` helper
(moved here from the deleted `watch`). Uses `serde_json`. No laterm imports.

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
import `ipc`, `hook`, `convo`, `mathscan`, or `graphics`.

**`graphics`** — `select() -> Option<Protocol>`. Tries kitty (env-based),
then imgcat (env-based), then Sixel (DA1 tty round-trip — probed last). Returns
`None` when none is supported (main fails loud). The kitty and imgcat encoders
are **private functions inside this module**, not separate files:
- *kitty* — `kitty_supported` (checks `KITTY_WINDOW_ID`, `TERM` containing
  `kitty`/`ghostty`, `TERM_PROGRAM == "ghostty"`) and `kitty_encode(png, rows)`,
  which emits the kitty graphics protocol: PNG (`f=100`) in ≤4096-byte base64
  chunks via `\x1b_G…\x1b\\` APC escapes, with `a=T,f=100,r=<rows>` on the first
  chunk — `rows` is always present, the proportional row count.
- *imgcat* — `imgcat_supported` (checks `TERM_PROGRAM == "iTerm.app"`,
  `LC_TERMINAL == "iTerm2"`, `TERM_PROGRAM == "WezTerm"`) and
  `imgcat_encode(png, rows)`, which emits
  `ESC ] 1337 ; File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len>:<base64> BEL`.

Imports `base64` and `sixel` (the only encoder that is its own module). The
returned `Protocol` holds an `encode_fn` pointer; `main` calls `encode(png, rows)`
through it and never names a specific protocol.

**`sixel`** — `supported() -> bool` sends a DA1 query (`\x1b[c`) via
`termbg::query_terminal` and parses the `\x1b[?<attrs>c` reply with `parse_da1`;
returns true when attribute `4` is present. The shared `query_terminal` helper
handles raw-tty/Console API on both unix and Windows, so DA1 works on Windows
Terminal. `set_background(r, g, b)` sets the opaque compositing color (call once
at startup after detecting the background). `detect_cell_height(timeout)` issues
a second `query_terminal` (`\x1b[16t`, parsed by `parse_cell_height`) and stores
the terminal's character-cell pixel height. `encode(png: &[u8], rows: u32)`
decodes the PNG to RGBA8 with the `png` crate, composites any partially
transparent pixels against the background color, **pads the image height up to a
multiple of the sixel band (6 rows) and — when the cell height is known — to a
multiple of the cell height** (so Windows Terminal fills no extra dark cells),
then emits a standard Sixel stream: DCS introducer + 1:1 raster attributes, RGB
color registers (0–100 scale), 6-row bands with RLE. Colors are collected
directly from pixels (bit-dropping loop to cap at 256 registers) — no
quantization crate. The `rows` parameter is currently **ignored** (Sixel has no
cell-based row scaling; the image renders at its padded native pixel size — see
Known Limitations). Uses an opaque background (terminal color or white) rather
than the transparent path kitty/imgcat use. Named constants `SIXEL_BAND`,
`SIXEL_CHAR_BASE`, and `COLOR_SCALE` replace the former magic 6 / 0x3F / 100.

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
Every `unsafe` block carries a `// SAFETY:` rationale (valid fd/handle,
initialized pointer+len, checked FFI returns). Platform implementations:
- **unix** — `query_terminal` opens `/dev/tty` and uses an RAII `RawModeGuard`
  (`cfmakeraw` on construct, prior termios restored on drop) so a panic in the
  I/O can't strand the tty in raw mode; `select(2)` for the read timeout.
  `raw_input` operates on stdin (fd 0); clears `ECHO | ICANON | IEXTEN`, keeps
  `OPOST` and `ISIG`; sets `VMIN=0 / VTIME=1`; its own `Drop` restores the mode.
- **Windows** — uses `windows-sys` Console API: `GetStdHandle`,
  `SetConsoleMode` with `ENABLE_VIRTUAL_TERMINAL_INPUT`, `WaitForSingleObject`
  for the timeout, `ReadConsoleA` for the reply. The request bytes are written
  with `WriteConsoleA` to the **console output handle** (not `std::io::stdout`),
  matching the unix `/dev/tty` write and keeping the stdout invariant. Handles
  are validated with `windows-sys`' `INVALID_HANDLE_VALUE` (no hand-rolled
  sentinel). `raw_input` operates on conin; clears `ENABLE_LINE_INPUT |
  ENABLE_ECHO_INPUT`, sets `ENABLE_VIRTUAL_TERMINAL_INPUT`; leaves output mode
  untouched; restores on drop. Full OSC 11 parity — not a stub.
Used once at startup to pick a contrasting glyph color (`query`); `raw_input`
is used by `main`'s manual-input read loop.

**`logging`** — Configures structured, leveled logging. Logging is opt-in:
path is resolved by `main` from `--log` only (no default). When a path is
given, file output at mode 0600, append. Acquires an exclusive writer lock for
the process lifetime (unix: advisory `flock(LOCK_EX | LOCK_NB)` via `libc`;
windows: `share_mode(FILE_SHARE_READ)` via std `OpenOptionsExt`, with
`FILE_SHARE_READ` taken from `windows-sys` rather than a hand-rolled literal —
no new dependency). A second instance that fails to acquire the lock prints one
warning to stderr and disables logging for that instance; readers (e.g.
`tail -f`) are unaffected. Level controlled by `LATERM_LOG_LEVEL` when logging
is enabled. If the file cannot be opened, prints one warning to stderr and
disables logging for the run. Never writes to stdout.

---

## Key Architecture Constraints

These are invariants from ARCHITECTURE.md. Violating any is a blocking defect.

### Isolation constraints

- **`ipc`, `hook`, `convo`, and `mathscan` import no other laterm modules.**
  Each depends only on the standard library and (where noted) one external crate
  (`hook`/`convo` use `serde_json`). `ipc` and `hook` do not import each other —
  `main` sequences them (`ipc` yields a payload → `hook` parses → `main` maps
  role to `feed`'s `EntryStyle`). `sixel` calls `termbg::query_terminal` (DA1 +
  cell-height probes); that is its only laterm import.
- **stdout is written only by `main` and the output-feed modules (`feed`,
  `input`).** `main` writes the plain-color startup line and the optional
  missing-dir warning; `feed`/`input` write the rendered feed. The `--hook`
  forwarder (`ipc` client) writes NOTHING to stdout at all. No other module may
  write to `std::io::stdout` — `ipc`, `hook`, `convo`, `mathscan`, `render`,
  `graphics`, `sixel`, `termbg`, and `logging` must not. Logging goes to file or
  stderr.
- **RaTeX crates confined to `render`.** No other module imports
  `ratex-parser`, `ratex-layout`, `ratex-render`, or `ratex-types`.
- **Graphics protocols confined.** The kitty and imgcat encoders are private
  functions inside `graphics`; `sixel` is imported only by `graphics`. `main`
  (and `feed`) pick/encode through `graphics::select` / `Protocol::encode`.
  `render` knows nothing of the output protocol — it only produces PNG bytes +
  height.

### Dependency direction

```
main
  |
  +---> ipc                   (UDS transport: listener producer + --hook client; std only)
  +---> hook                  (pure payload parser -> (role, text); serde_json)
  +---> convo                 (catch-up historical jsonl parse; serde_json)
  +---> feed ----> mathscan
  |          \---> render  (ratex-parser, ratex-layout, ratex-render, ratex-types)
  |          \---> graphics --> sixel (png; calls termbg::query_terminal)
  |          |                  (kitty/imgcat encoders are private fns in graphics; base64)
  |          \---> logging
  +---> input ---> feed
  |          \---> termbg
  +---> termbg
  +---> logging

(all modules may use logging)
```

No cycles. `input → feed` is the only inter-feed edge; `feed` does not depend on
`input`. Direction is strictly downward.

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
informed the mathscan edge-case list; Rust fuzz tests for `mathscan`, `convo`,
and `hook` are a good addition. `hook` and `ipc::socket_path` are pure/
deterministic and unit-testable without a live socket: parse fixture payloads
(UserPromptSubmit/Stop, cwd match vs mismatch, malformed JSON) and assert the
derived socket path is stable for a given cwd. The `--hook` client's
silent-exit-on-no-listener and stdout-silence are cheap integration checks.

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
| `serde` v1 | `convo`, `hook` |
| `serde_json` v1 | `convo`, `hook`, `main` (settings.json merge for `--install-hooks`) |
| `base64` v0.22 | `graphics` |
| `png` v0.17 | `sixel` (PNG→RGBA decode; no sixel or quantization crate — encoder is in-house) |
| `chrono` v0.4 | `main` (RFC3339 timestamp parsing for `--catch-up`) |
| `ctrlc` v3 | `main` (cross-platform signal handling, MIT/Apache-2.0) |
| `libc` v0.2 | `termbg` (unix only — `[target.'cfg(unix)']`): termios raw mode, `select(2)`, `ioctl(TIOCGWINSZ)`; `logging` (unix only) |
| `windows-sys` v0.59 | `termbg` (Windows only — `[target.'cfg(windows)']`): Console API for OSC 11 / DA1 / cell-size queries (`WriteConsoleA` for the request, `ReadConsoleA` for the reply), `GetConsoleScreenBufferInfo` for terminal width; `logging` (Windows only): `FILE_SHARE_READ` (feature `Win32_Storage_FileSystem`) for the log share mode |

The `ipc` UDS transport adds NO new crate — it uses `std::os::unix::net`. The
hook re-architecture introduces no external dependency.

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

The log directory is `~/.claude/projects/<cwd>` where every non-alphanumeric
character (anything not in `[A-Za-z0-9]`) in the absolute working directory path
is replaced 1:1 by `-`. For example, `/Users/benn/projects/laterm` becomes
`~/.claude/projects/-Users-benn-projects-laterm`;
`/Users/benn/projects/agent_convos/ai-race` becomes
`~/.claude/projects/-Users-benn-projects-agent-convos-ai-race` (note the `_`
becomes `-`); and on Windows `C:\Users\benn\projects\laterm` becomes
`~/.claude/projects/C--Users-benn-projects-laterm`. This is the same convention
used by Claude Code (`ipc::mangle_dir` must track it byte-for-byte); do not change
it unilaterally.

### Hooks must be installed for the live path

The live path requires Claude Code to be configured to push turns. Run `laterm
--install-hooks` once (per project, or `--global`) so Claude Code invokes
`laterm --hook <event>` on each turn. Without the hooks installed, only
`--catch-up` and manual paste render anything — the renderer will sit idle on an
empty socket. `--install-hooks` writes/merges `settings.json`; that is the ONE
configuration write laterm makes and is not part of monitoring operation (the
non-interfering-source invariant still holds — laterm never writes transcripts).

### Socket rendezvous and stale sockets

The `--hook` client and the renderer's listener must derive the SAME socket path
or turns never arrive. Both call `ipc::socket_path(cwd)`: `$XDG_RUNTIME_DIR` (else
`~/.cache/laterm/`) + the dash-mangled cwd + `.sock`. The renderer passes its own
watched working directory; the client passes the payload's `cwd` field (the
project dir Claude Code reports) — these are the same string, so both land on the
same socket. Keep the dash-mangling a single shared helper with the log-dir
derivation — do not fork it. The listener
must remove a stale socket file (left by a prior crash) before binding, and
remove its own on shutdown, or a restart hits `EADDRINUSE`.

### A hook with no running renderer is a silent no-op

If a hook fires but no renderer is listening, the `--hook` client exits 0 and
renders nothing — by design, so it never blocks or fails Claude Code. Turns
emitted while laterm is not running are not shown live (they remain visible to
`--catch-up`). Likewise the `--hook` forwarder must print NOTHING to stdout —
a `UserPromptSubmit` hook's stdout would be injected into the model's context.

### Pre-tool-call assistant prose is not rendered live

The `Stop` hook delivers only the turn's FINAL assistant text
(`last_assistant_message`). Assistant prose emitted BEFORE a tool call within the
same turn is not delivered by the hook and is not echoed live. `--catch-up` on
the completed transcript does see the full turn. This is an accepted fidelity
trade (the rejected OTEL alternative truncates at 60 KB).

### Windows live path is not yet available

The `ipc` transport is a Unix-domain socket. The Windows named-pipe transport is
a planned follow-up; until it lands, `--hook`/`--install-hooks`/live rendering
are unix-only. Paste and Windows graphics/termbg paths are unaffected.

### Missing log directory is not an error

If the derived log directory does not exist when laterm starts, laterm prints a
one-line plain-color warning (no conversation log yet — paste still works) and
keeps running: the `ipc` listener still accepts live hook payloads, and only
`--catch-up` needs the dir. This is normal when laterm is started before Claude
Code has opened the project; laterm does not exit.

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
