# LaTerm Architecture Design

A Claude Code sidecar that receives conversation turns for the current project
— pushed live by Claude Code hooks over a Unix-domain socket — and renders
LaTeX math expressions as inline images in a separate graphics-capable terminal
window (kitty graphics protocol, iTerm2 imgcat, or Sixel).

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

3. **Non-interfering source.** laterm never writes to, modifies, or interferes
   with the Claude Code process or its transcript files. It is no longer a
   passive observer, though: it REQUIRES Claude Code to be configured — via the
   two hooks (see `--install-hooks`) — to push turns to it. Its live inputs are
   hook payloads received over a Unix-domain socket plus its own stdin (manually
   pasted text). Its historical input (`--catch-up`) reads completed `*.jsonl`
   transcripts read-only. laterm never writes back to the transcripts or the
   Claude Code process. (`--install-hooks` writes a `settings.json`, but that is
   an explicit, user-invoked configuration command, not part of monitoring
   operation — see section 2.)

4. **Hook-push live turns; historical catch-up only.** Live turns are NOT
   tailed from the transcript. Recent Claude Code (verified on v2.1.220) writes
   the per-session `*.jsonl` transcript asynchronously — it lags in-memory state,
   so a live tailer misses or lags the current turn. Instead, Claude Code is
   configured with two hooks (`UserPromptSubmit`, `Stop`) that push turn text to
   laterm over a Unix-domain socket as each turn happens. laterm renders each
   received hook payload exactly once as it arrives; there is no live file
   reading. Historical replay is the one file-reading path: `--catch-up[=<MINS>]`
   reads the COMPLETED `*.jsonl` transcript(s) in the log dir and replays entries
   whose RFC3339 `timestamp` falls within the last MINS minutes (default 5),
   across all files, in chronological order. Catch-up is historical-only and
   strictly distinct from the live hook path — it never touches the socket.

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
   entry (one hook event — a `UserPromptSubmit` prompt or a `Stop` assistant
   message — one catch-up transcript entry, or one paste) is prefixed with a
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
   separator rule, and BEL); no other module writes stdout, and the `--hook`
   forwarder process (`ipc` client) writes NOTHING to stdout at all (stdout from
   a `UserPromptSubmit` hook is injected into the model's context). **Live echo
   is per-hook-event:** `UserPromptSubmit` yields one user entry, `Stop` yields
   one assistant entry. The `Stop` payload carries only the turn's FINAL
   assistant text (`last_assistant_message`); assistant prose emitted BEFORE a
   tool call within the same turn is not delivered and is therefore not echoed
   (see section 5, Known Limitations). Everything about markers, tinting,
   block-vs-inline, and proportional row sizing is unchanged.

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
  main.rs          -- wiring: modes (normal / --hook / --install-hooks), derive log dir + socket path, protocol select, ipc listener loop, signal handling
  feed.rs          -- output/render orchestration: markers, tinted prose, math images, RenderCtx
  input.rs         -- manual paste-only stdin: PasteParser, separator rule, BEL
  ipc.rs           -- UDS transport: socket-path derivation, listener accept-loop, --hook forward client (unix now; windows named-pipe later)
  hook.rs          -- pure hook-payload parser: UserPromptSubmit/Stop JSON -> (role, text), cwd filter
  convo.rs         -- historical jsonl parser (catch-up only): extracts text from user/assistant entries
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
- Responsibility (wiring only). Three modes, dispatched from CLI flags:
  1. **`--hook <event>` (forward-and-exit).** A thin forwarder: read the hook's
     JSON from stdin, derive the socket path from the payload's `cwd` field (the
     authoritative project dir — `hook::payload_cwd` does a minimal parse of just
     `cwd`, then `ipc::socket_path(cwd)`), and write those raw bytes over the
     socket to the running laterm renderer, exit 0. Deriving from the payload
     `cwd` (not the hook process's own working directory) guarantees the client
     and the matching renderer rendezvous on the same socket regardless of where
     Claude Code invokes the hook from. In this mode `main` does NO protocol select, NO
     background detection, NO rendering, NO theme, NO paste thread — it only
     forwards. It writes NOTHING to stdout (stdout from a `UserPromptSubmit`
     hook is injected into the model's context). If no listener is present
     (socket missing or connection refused) it exits 0 SILENTLY — it must never
     block or fail Claude Code. This mode is dispatched before any other work.
  2. **`--install-hooks [--project|--global]` (configure-and-exit).** Merge the
     two hook entries (see the hook JSON below) into the appropriate
     `settings.json` without clobbering existing settings, then exit.
  3. **normal (the renderer).** Parse CLI flags (`-C`/`--cwd`, `--log`,
     `--catch-up`, `--help`/`--version`). If `-C`/`--cwd <PATH>` is given,
     `set_current_dir(PATH)` **before** deriving the log dir and socket path, so
     the existing `current_dir()`-based derivation produces the same canonical
     absolute path the OS gives Claude Code and the dir-name mangling matches by
     construction (no manual canonicalization). Initialize logging. Derive the
     Claude Code log directory and the socket path (both from the possibly-
     changed working directory). Print the startup line (see below). Select a
     graphics protocol via `graphics::select` **once**, exit immediately (clear
     error) if none of kitty, imgcat, or Sixel is supported, and wrap it in an
     `Arc` shared by catch-up, the listener loop, and the reader thread (so the
     sixel DA1 probe runs exactly once). Detect the terminal background via
     `termbg::query` and configure the renderer theme (`apply_theme`). Build a
     `feed::RenderCtx` once — its constructor renders the reference capital "X"
     (default 42 px if it fails) to derive the block-threshold (1.5× reference)
     and the proportional row sizing, and carries the protocol. Spawn the stdin
     reader thread (`input::read_input`, given the `RenderCtx` and the shared
     output mutex). If `--catch-up` was given, replay recent history from the
     completed transcripts (grouped by source entry) via `convo::extract` +
     `feed::emit_entry` before starting the listener. Start the `ipc` listener
     loop: for each received payload, `hook::parse` it, drop it if its `cwd`
     does not match the watched cwd, map the parsed role to the `feed`
     EntryStyle (user → `USER_STYLE`, assistant → `ASSISTANT_STYLE`), and
     `feed::emit_entry` under the output mutex. Handle SIGINT/SIGTERM to stop
     the listener cleanly and remove the socket file on exit.
- Log directory derivation: `~/.claude/projects/<cwd>` where every `/` in the
  absolute working directory path is replaced by `-`.
- Socket path derivation: delegated to `ipc` (a single shared helper, so the
  `--hook` client and the listener always agree). Under `$XDG_RUNTIME_DIR` if
  set, else `~/.cache/laterm/`; the filename is the same dash-mangled working-
  directory string used for the log dir plus a `.sock` suffix. The dash-mangling
  is a single shared helper — it must NOT be duplicated between the log-dir and
  socket-path derivations.
- `--install-hooks` writes, for each of `UserPromptSubmit` and `Stop`, an entry
  invoking the current executable with `--hook <event>`. The command is the
  absolute path from `current_exe()` (so the hook fires regardless of PATH). The
  exact JSON written (merged additively under the top-level `hooks` key of the
  target `settings.json`) is:

  ```json
  {
    "hooks": {
      "UserPromptSubmit": [
        { "hooks": [ { "type": "command", "command": "<abs-path>/laterm --hook UserPromptSubmit" } ] }
      ],
      "Stop": [
        { "hooks": [ { "type": "command", "command": "<abs-path>/laterm --hook Stop" } ] }
      ]
    }
  }
  ```

  `--project` targets `.claude/settings.json` (repo-local); `--global` targets
  `~/.claude/settings.json`; the default when neither is given is `--project`.
  Merge is additive and idempotent: existing top-level keys and existing hook
  entries are preserved, and a matching laterm entry is not duplicated on re-run.
- Startup line: after the protocol is selected and the log dir is derived (and
  after any `-C` chdir), `main` writes ONE plain-color line to stdout (no SGR
  role color, no role marker): `laterm <version> monitoring <dir>/`, where
  `<version>` is `CARGO_PKG_VERSION` and `<dir>` is the derived project log dir
  tilde-collapsed (the home prefix — HOME on unix / USERPROFILE on windows, the
  same source `project_log_dir` uses — replaced by `~`; otherwise the absolute
  path), with a trailing `/`. The collapse is a pure helper (`display_dir`).
- Missing-dir warning: if the derived dir does not exist at startup, `main`
  writes a second plain-color stdout line right under the startup line phrased
  as not-yet (paste still works), e.g. `  no conversation log for this directory
  yet — paste to render, or start Claude Code here`. The dir only gates
  `--catch-up`; live rendering waits on the `ipc` listener regardless, and
  laterm does not exit.
- Must NOT contain: rendering, parsing, marker, protocol, or transport logic.
  Parsing lives in `hook`/`convo`, transport in `ipc`, rendering in `feed`. This
  is wiring only.

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
- Must NOT import: `input`, `ipc`, `hook`, `convo`, `termbg`.

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
  the shared output mutex so they don't interleave with the listener loop.
- Imports: `feed`, `termbg`, `logging`. `input → feed` (no cycle).

**`ipc`**
- Responsibility: The Unix-domain-socket transport. Three pieces:
  1. **Socket-path derivation** (shared by the listener and the `--hook`
     client, so they rendezvous deterministically): `socket_path()` returns the
     socket path for the current working directory — under `$XDG_RUNTIME_DIR`
     if set, else `~/.cache/laterm/`, filename = the dash-mangled working-dir
     string (the SAME mangling `main` uses for the log dir — a single shared
     helper, not a copy) plus `.sock`. Creates the parent directory if needed.
  2. **Listener** (replaces `watch`'s producer role): bind a `UnixListener` at
     the socket path — removing any stale socket file left by a prior crash
     before binding — and run an accept loop. Each accepted connection is read
     to EOF (one raw JSON payload per connection) and the payload is handed to
     the main loop (channel or callback, matching `watch`'s old producer shape).
     Connections are handled one at a time, in arrival order, so a
     `UserPromptSubmit` is delivered before its paired `Stop`. Observes the
     cancellation flag so SIGINT/SIGTERM breaks the loop; removes the socket
     file on shutdown.
  3. **`--hook` client** (the forwarder half): `send(socket_path, bytes)`
     connects and writes the raw stdin bytes, and `forward(cwd, bytes)` wraps
     `socket_path(cwd)` + `send`. The socket path is derived from the payload's
     `cwd` field (parsed by `hook`, sequenced by `main` — `ipc` stays free of any
     laterm import and does no JSON parsing), so the client reaches exactly the
     socket the matching renderer listens on. On any connect/write failure
     (socket missing, connection refused, listener gone) it returns quietly so
     `main` exits 0 — never block, never fail Claude Code. Writes NOTHING to
     stdout under any circumstance (the stdout invariant for the forwarder).
- Platform-split like `termbg`: unix uses `std::os::unix::net` now; the Windows
  transport (a named pipe) is a planned follow-up (see section 5) — the live
  hook path is not yet available on Windows.
- No dependencies on other laterm modules. Uses only the standard library (no
  new external crate).

**`hook`**
- Responsibility: A PURE payload parser (mirrors `convo`'s old purity). Parse a
  hook stdin JSON payload into typed structs and return the same neutral
  `(role, text)` shape `convo::extract` returns — `role` is a neutral parser
  enum (user / assistant), NOT `feed`'s `EntryStyle`; `main` maps role →
  `EntryStyle`, keeping this module free of any laterm-module import. Two events:
  - `UserPromptSubmit` → `(user, prompt)` — `prompt` is the user's message text.
  - `Stop` → `(assistant, last_assistant_message)` — the FULL, untruncated final
    assistant text for the turn.
  Also exposes the `cwd` from the payload so `main` can filter (a payload whose
  `cwd` does not match the watched working directory is dropped — a
  boundary/contract check against a globally-installed hook firing for another
  project). `parse(bytes, watched_cwd)` takes the renderer's watched cwd and
  drops on mismatch; a separate `payload_cwd(bytes)` does a minimal parse of just
  the `cwd` for the `--hook` forwarder's socket-path derivation (no event/content
  validation — the client forwards raw bytes regardless). Returns None/empty for
  an unrecognized event, empty content, a `cwd` mismatch, or parse failure.
  Never panics.
- Uses `serde` (derive) and `serde_json`.
- No dependencies on other laterm modules.

**`convo`** (catch-up historical path only)
- Responsibility: Parse one complete jsonl line from a COMPLETED transcript for
  `--catch-up`. This is the only surviving jsonl-parsing path — the live path is
  hooks, so `convo` is no longer on it. Extract text content from
  `message.content` for entries whose top-level `type` is `"user"` or
  `"assistant"`. Content may be a JSON array of blocks (return `type:"text"`
  blocks only) or a plain JSON string. Return empty/None for any other entry
  type, empty content, or parse failure. Never panic. Owns the `is_jsonl(path)`
  file-membership helper used by catch-up's directory scan (it moved here from
  the deleted `watch` module — a pure path predicate, no laterm imports).
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
- Must NOT import: `ipc`, `hook`, `convo`, `mathscan`, `graphics`, `feed`, `input`.

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
- Must NOT import: `render`, `mathscan`, `convo`, `ipc`, `hook`, `feed`, `input`.

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
  +---> ipc                   (UDS transport: listener producer + --hook client; std only)
  +---> hook                  (pure payload parser -> (role, text); serde_json)
  +---> convo                 (catch-up historical jsonl parse; serde_json)
  +---> feed ----> mathscan
  |          \---> render    (ratex-parser, ratex-layout, ratex-render, ratex-types)
  |          \---> graphics  (base64; kitty/imgcat encoders are private fns here)
  |          |          \--> sixel (png; calls termbg::query_terminal)
  |          \---> logging
  +---> input ---> feed
  |          \---> termbg
  +---> termbg
  +---> logging

(all modules may use logging)
```

Dependency direction is strictly downward. The only inter-feed edge is
`input → feed`; `feed` does not depend on `input`, so there is no cycle.
`ipc`, `hook`, `convo`, and `mathscan` depend only on the standard library
(`hook`/`convo` also use `serde_json`); they do not import any other laterm
module. `ipc` is transport-only and `hook` is parse-only: they do not import
each other — `main` drives the sequence (`ipc` yields a raw payload → `hook`
parses it → `main` maps role to `feed`'s `EntryStyle` → `feed` renders). `feed`
orchestrates `mathscan`/`render`/`graphics`; `input` reads stdin via `termbg`
and renders via `feed`. `termbg` uses `libc` (unix) or `windows-sys` (Windows)
for raw-mode terminal I/O. `render` is the only module that imports the RaTeX
crates. `main` uses the `ctrlc` crate for cross-platform signal handling
(SIGINT/SIGTERM) and selects a protocol via `graphics::select` exactly once.

### Dependency Justification

| Dependency | Module | Justification |
|---|---|---|
| `ratex-parser` v0.1.9 | `render` | KaTeX-compatible LaTeX parser; pure Rust, no external runtime |
| `ratex-layout` v0.1.9 | `render` | TeX box-model layout engine for the RaTeX pipeline |
| `ratex-render` v0.1.9 (`embed-fonts`) | `render` | PNG renderer; `embed-fonts` bundles KaTeX fonts into the binary, eliminating external font dependencies |
| `ratex-types` v0.1.9 | `render` | Shared type definitions required to wire the RaTeX pipeline stages |
| `serde` v1 | `convo`, `hook` | Derive macros for JSON deserialization |
| `serde_json` v1 | `convo`, `hook`, `main` | Parsing `.jsonl` transcript entries (`convo`), hook payloads (`hook`), and reading/merging `settings.json` for `--install-hooks` (`main`) |
| `base64` v0.22 | `graphics` | Encoding PNG bytes for kitty and imgcat image protocols |
| `png` v0.17 | `sixel` | PNG→RGBA8 decode for the Sixel encoder; no sixel or quantization crate is used — the encoder is in-house |
| `chrono` v0.4 | `main` | RFC3339 timestamp parsing for `--catch-up` window filtering |
| `ctrlc` v3 | `main` | Cross-platform SIGINT/SIGTERM handler (MIT/Apache-2.0) |
| `libc` v0.2 | `termbg` (unix only, `[target.'cfg(unix)']`); `logging` (unix only) | termios raw mode + `select(2)` for OSC 11 background query; `ioctl(TIOCGWINSZ)` for terminal width; `flock(LOCK_EX \| LOCK_NB)` for the exclusive log-file writer lock |
| `windows-sys` v0.59 | `termbg`, `logging` (Windows only, `[target.'cfg(windows)']`) | Console API (`GetStdHandle`, `SetConsoleMode`, `WaitForSingleObject`, `ReadConsoleA`, `WriteConsoleA`) for OSC 11 / DA1 / cell-size queries; `GetConsoleScreenBufferInfo` for terminal width; `INVALID_HANDLE_VALUE` for handle validation; `FILE_SHARE_READ` (feature `Win32_Storage_FileSystem`) for the log-file share mode |

The `ipc` UDS transport (listener + `--hook` client) adds NO external crate: it
uses `std::os::unix::net::{UnixListener, UnixStream}`. The Windows named-pipe
follow-up is expected to reuse `windows-sys`, already present. No new dependency
is introduced by the hook re-architecture.

No other external dependencies are permitted without updating this table and
providing justification.

---

## 3. Acceptance Criteria

Observable behavioral outcomes that must be true when implementation is
complete. Organized by component, in implementation priority order.

### Sidecar startup (Priority 1)

- `laterm` spawns no child process. Accepted flags: `-C`/`--cwd <PATH>`,
  `--log <PATH>`, `--catch-up[=<MINS>]` (bare = 5 minutes),
  `--hook <event>` (forward-and-exit; `event` is `UserPromptSubmit` or `Stop`),
  `--install-hooks [--project|--global]` (configure-and-exit; default
  `--project`), `--version`/`-V`, `--help`/`-h`.
  Unknown flags or a missing/empty `--log`, `--cwd`, `-C`, or `--hook` argument
  exit non-zero with a usage message to stderr. (`--hook` mode is the exception
  to stderr-on-error: a missing listener is exit 0 and silent — see below.)
- `-C`/`--cwd <PATH>` sets the working directory used to derive both the watched
  log dir and the socket path, via `set_current_dir(PATH)` before derivation
  (footgun-free: the existing derivation then mangles the OS-canonical absolute
  path). A `set_current_dir` failure prints an error to stderr and exits
  non-zero.
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
  warning line and keeps running (the `ipc` listener still accepts hook
  payloads; only `--catch-up` needs the dir) rather than exiting.
- SIGINT and SIGTERM stop the `ipc` listener loop, remove the socket file, and
  cause laterm to exit 0.
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

### Live hook transport (Priority 1)

- The `--hook <event>` client and the renderer's listener derive the SAME socket
  path from the same working directory (socket rendezvous): the client connects
  to exactly the socket the matching renderer is listening on.
- A `--hook <event>` invocation reads its stdin JSON and forwards the raw bytes
  over the socket, then exits 0. It writes NOTHING to stdout under any condition
  (verified for `UserPromptSubmit`, whose stdout would be injected into the
  model's context).
- If no listener is present (socket missing, or connection refused), the
  `--hook` client exits 0 silently — it never blocks and never fails Claude Code.
- The renderer's listener binds its socket path (removing a stale socket file
  first if present), accepts connections one at a time in arrival order, reads
  each payload to EOF, and renders it; the socket file is removed on shutdown.
- A received payload whose `cwd` does not match the renderer's watched working
  directory is dropped (a globally-installed hook renders only matching turns).
- `UserPromptSubmit` produces one user entry (`prompt` text); `Stop` produces
  one assistant entry (`last_assistant_message` text) — one marker-pair each.
- `--install-hooks` writes the two hook entries (invoking `<current-exe> --hook
  UserPromptSubmit` and `... --hook Stop`) into the target `settings.json`
  (`--project` → `.claude/settings.json`, `--global` → `~/.claude/settings.json`,
  default `--project`), merging without clobbering existing settings and without
  duplicating an already-present laterm entry on re-run. It then exits.

### Catch-up historical parser (Priority 2)

The catch-up path (only) parses completed `*.jsonl` transcript lines via `convo`:

- Entries with `type` other than `"user"` or `"assistant"` return None/empty.
- `message.content` as a JSON array: only `type:"text"` blocks are returned.
- `message.content` as a plain JSON string: returned as a single segment.
- Malformed JSON returns None/empty without panicking.

### Hook payload parser (Priority 2)

- A `UserPromptSubmit` payload parses to `(user, prompt)`; a `Stop` payload
  parses to `(assistant, last_assistant_message)` (full, untruncated).
- An unrecognized `hook_event_name`, empty content, or a `cwd` that does not
  match the watched directory yields None/empty (the payload is dropped).
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
- `--catch-up[=<MINS>]` (historical-only, distinct from the live hook path):
  before the `ipc` listener starts, all completed `*.jsonl` files in the log
  directory are scanned; entries whose RFC3339 `timestamp` is at or after
  `now - MINS minutes` are collected, sorted by timestamp, and rendered in
  order. Catch-up reads files read-only and never touches the socket. Absent or
  unparseable timestamps are excluded. (Because live turns arrive by hook, not
  by tailing, there is no file-offset bookkeeping and no double-emit risk between
  catch-up and the live path.)
- `make release` (or `cargo build --release`) produces a self-contained binary
  with fonts embedded. `make dist` builds release binaries for all three
  release targets into `dist/`.
- `make test` (or `cargo test`) runs unit tests.

---

## 4. Data Flow Summary

```
Claude Code process (per turn)
  |
  | UserPromptSubmit / Stop hook fires:  laterm --hook <event>
  |   (stdin = hook JSON; command installed via --install-hooks)
  v

[ipc --hook client]  cwd = payload.cwd (hook::payload_cwd); connect
  |                    socket_path(cwd); write raw stdin bytes; exit 0
  |                    (no listener -> exit 0 silently, nothing to stdout)
  | raw JSON payload over the Unix-domain socket
  v

[ipc listener accept-loop]  (renderer process; replaces the old watch producer)
  |
  | one raw hook payload per connection
  v

[hook::parse]
  |
  | (role, text): UserPromptSubmit -> (user, prompt)
  |               Stop             -> (assistant, last_assistant_message)
  | drop if cwd != watched cwd, or unrecognized event / empty / parse failure
  v

[main maps role -> feed EntryStyle: user->USER_STYLE, assistant->ASSISTANT_STYLE]
  |
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

catch-up (historical, no socket): reads completed *.jsonl files via
          convo::extract; each replayed entry's segments are grouped and emitted
          with a single feed::emit_entry call, so framing is byte-identical to
          the live hook path (one marker-pair per entry).
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

- **Pre-tool-call assistant prose is not rendered (live path).** The `Stop`
  hook delivers only the turn's FINAL assistant text (`last_assistant_message`).
  Assistant prose emitted BEFORE a tool call within the same turn is not
  delivered by the hook and is therefore not echoed live. This is an accepted
  fidelity trade: the rejected alternative (OTEL log events) truncates content
  at 60 KB by default — a dealbreaker for long, math-dense answers — and requires
  a heavyweight OTLP receiver. `--catch-up` on the completed transcript does see
  the full turn, including pre-tool-call prose.

- **Windows live-hook path not yet available.** The `ipc` transport is a Unix-
  domain socket (unix-first). The Windows transport (a named pipe) is a planned
  follow-up; until it lands, `--hook`/`--install-hooks` and live rendering are
  unix-only on Windows. Paste and (Windows) graphics detection are unaffected.

- **A hook with no running renderer is silently a no-op.** If Claude Code fires
  a hook but no laterm renderer is listening on the derived socket, the `--hook`
  client exits 0 and renders nothing — by design, so it never blocks or fails
  Claude Code. Turns emitted while laterm is not running are simply not shown
  live (they remain visible to `--catch-up` afterward).

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
