# ARCHITECTURE.md — LaTerm

**How this implementation satisfies [SPEC.md](SPEC.md).** SPEC.md defines what
LaTerm must do; this document defines the Rust program that does it. Everything
here is a choice and could be made differently without changing what LaTerm is.

`R-n.m` cites a SPEC.md requirement. **This document does not restate
requirements** — where a mechanism exists to satisfy one, it cites it. Behavior
specified here and not in SPEC.md is a filing error, not a second source of
truth.

---

## 1. Design Decisions

### 1.1 Hook push over transcript tailing

Live turns arrive as host-agent hook invocations over a Unix-domain socket
(`R-2.1`, `R-2.2`).

Claude Code writes the `*.jsonl` transcript asynchronously (observed v2.1.220),
so it lags in-memory state and a tailer misses or trails the turn it is meant to
show live. This replaced an earlier tailing design; tailing survives only in
`--catch-up` (`R-3`), where the file is already complete and the lag is
irrelevant.

**Rejected — OpenTelemetry log events.** Claude Code can emit turn content as
OTEL logs. Rejected for two reasons: the exporter truncates event bodies at 60 KB
by default, which long, math-dense answers exceed; and it requires standing up an
OTLP receiver, a heavyweight dependency for a single-binary sidecar. The cost of
hooks is SPEC.md §5.1 — accepted.

### 1.2 Unix-domain socket as the transport

A UDS costs no external crate (`std::os::unix::net`), is filesystem-addressable
(so the `R-2.3` rendezvous reduces to deriving a path both sides compute the same
way), is inherently local, and carries filesystem permissions. A TCP port would
need port allocation and a discovery mechanism, and would be network-reachable.

Path: `$XDG_RUNTIME_DIR` (else `~/.cache/laterm/`) + the dash-mangled project
directory + `.sock`. **The mangling is the same helper that derives the
transcript directory** (`ipc::mangle_dir`), not a copy — see §5.3. Unix only
(§11.1).

### 1.3 Runtime reference-X sizing

`R-7.2`/`R-7.3` require image size and inline-vs-block layout to track the
terminal's text size. Rather than hard-code pixel thresholds, the renderer
measures its own output once — a capital `X` at startup — and expresses both the
size formula and the block threshold as multiples of that height. The numbers
then track RaTeX's actual metrics across a version bump. Fallback when the
reference render fails: **42 px**.

### 1.4 In-house Sixel encoder

`sixel.rs` encodes directly rather than pulling a Sixel crate plus a color
quantizer. The format is simple enough that the encoder is smaller than the
dependency surface it replaces, and it lets the `R-8.3` compositing be handled
exactly. Palette reduction is a bit-dropping loop capped at 256 registers —
adequate for near-monochrome glyph rendering.

### 1.5 One `Protocol` handle, selected once

`R-8.5` forbids repeating a terminal round-trip per image. `graphics::select`
returns a `Protocol` carrying an `encode_fn` pointer; that single value is wrapped
in an `Arc` shared by catch-up, the listener loop, and the paste thread. No call
site names a specific protocol — `feed` calls `ctx.proto.encode(png, rows)` and
does not know which it got.

---

## 2. Crate Layout

```
src/
  main.rs      wiring: mode dispatch, derivation, listener loop, signals
  feed.rs      output/render orchestration: markers, tint, images, RenderCtx
  input.rs     manual paste-only stdin: PasteParser, separator rule, BEL
  ipc.rs       UDS transport: path derivation, listener, --hook client
  hook.rs      pure hook-payload parser -> Parsed { role, text, cwd }
  convo.rs     historical jsonl parser (catch-up only) + is_jsonl()
  mathscan.rs  LaTeX delimiter scanner -> flat Vec<Segment>
  render.rs    RaTeX pipeline -> (png_bytes, height_px)
  graphics.rs  protocol selection + kitty/imgcat encoders (private fns)
  sixel.rs     Sixel encoder; supported() via DA1
  termbg.rs    OSC 11 / DA1 / cell-size queries; raw input; terminal width
  logging.rs   log configuration, file + stderr, level management
```

There are no `kitty.rs` / `imgcat.rs`. Both are private functions in
`graphics.rs` (`kitty_supported`/`kitty_encode`, `imgcat_supported`/
`imgcat_encode`) — each an environment predicate plus a formatter, stateless and
dependency-free. `sixel` is a module: an order of magnitude larger, and it needs
`termbg` for its probes.

---

## 3. Module Responsibilities

Import constraints are not repeated per module — see the graph in §4 and the
isolation invariant in §5.1.

### `main` — wiring only

Three modes, dispatched from CLI flags before any other work.

**`--hook <event>`** (`R-2.2`) — read stdin, `hook::payload_cwd` for a minimal
parse of just `cwd`, `ipc::forward(cwd, bytes)`, exit 0. Deliberately performs
*none* of the renderer's startup: no protocol select, background query, theme,
`RenderCtx`, or paste thread. Silent on failure (`R-2.4`) and on success
(`R-11.2`).

**`--install-hooks [scope]`** (`R-10`) — read the target `settings.json` with
`serde_json`, merge the two entries additively under the top-level `hooks` key,
write back, exit. Command string is `current_exe()` + ` --hook <event>`
(`R-10.2`).

**normal** — the renderer, in order:

1. Parse flags. On `-C`/`--cwd`, `set_current_dir(PATH)` **first** (`R-9.2`):
   every later derivation reads `current_dir()`, so it mangles the OS-canonical
   absolute path, and transcript directory and socket path agree by
   construction. No manual canonicalization is needed.
2. Initialize logging (`R-12`).
3. `graphics::select` once; error-exit if `None` (`R-8.1`).
4. `apply_theme` — `main`'s own function: `termbg::query` the background, pick a
   contrasting glyph color, hand both to `render::set_theme` (`R-8.2`, `R-8.4`).
   Under sixel it also calls `sixel::set_background` and
   `sixel::detect_cell_height` (`R-8.3`).
5. Derive the transcript directory and, via `ipc`, the socket path — both from
   the possibly-changed working directory, through one shared mangling helper.
   Protocol and theme come first deliberately: neither reads the working
   directory, so nothing before this point can observe a stale one.
6. Startup line, plus the missing-directory warning if applicable (`R-9.6`,
   `R-9.7`).
7. Register the `ctrlc` handler, setting the shared cancellation flag (`R-13.1`).
8. Build `feed::RenderCtx` once — its constructor performs the reference-X render
   (§1.3) and carries the protocol.
9. Spawn the stdin reader thread (`input::read_input`, given the output mutex,
   the `RenderCtx`, and the cancellation flag).
10. If `--catch-up`: `convo::parse` over the transcript files, grouped by source
    entry, each group through one `feed::emit_entry` (`R-3.5`).
11. Run the `ipc` listener loop. Per payload: `hook::parse` → drop on `cwd`
    mismatch (`R-2.6`) → map the parser's role enum to `feed`'s `EntryStyle` →
    `feed::emit_entry` under the output mutex. Remove the socket file on exit.

The role→`EntryStyle` mapping in step 11 lives here specifically so `hook` can
stay free of laterm imports (§5.1).

### `feed` — output and render orchestration

Owns the `EntryStyle` role markers (`USER_STYLE`/`ASSISTANT_STYLE`/`PASTE_STYLE`
— the concrete strings and colors of `R-6.2`), `role_style`, the sizing constants
(`ROW_SCALE` = 1.25, the 1.5× block threshold, `rows_for`), `RenderCtx`, and the
`emit_entry`/`emit_segments` pipeline.

`emit_entry(style, texts, &RenderCtx)` walks each text's segment list: `Text`
verbatim, `Math` through `render::render` then `ctx.proto.encode(png, rows)`.
`RenderCtx` collapses three former parameters — block threshold, reference
height, protocol — into one value passed by reference. An `at_line_start` cursor
flag threaded through `emit_segments` decides close-marker placement (`R-6.3`);
it is set by a block image *and* by any text ending in a newline. The return
value is the number of math segments emitted, which `main` uses for the catch-up
log line.

Writes stdout **under the caller-held output mutex**, taking no inner stdout
lock — the mutex is the cross-thread serializer between the listener loop and the
paste thread. `emit_entry` clears the manual-separator armed state;
`arm_separator()` lets `input` re-arm and test it (`R-4.5`) without a
`feed → input` edge.

### `input` — manual paste-only stdin

Holds a `termbg::raw_input()` guard for its lifetime; enables bracketed paste
(`ESC[?2004h`) at startup and disables it (`ESC[?2004l`) on exit (`R-13.3`).

`PasteParser` is a byte-at-a-time state machine capturing between `ESC[200~` and
`ESC[201~`; captured text goes through `feed::emit_entry` with `PASTE_STYLE`, so
a paste is one entry regardless of line count (`R-4.1`). Enter writes the
separator rule (`R-4.4`, width from `termbg::term_width()`); printable keystrokes
produce the throttled BEL (`R-4.3`); escapes and control bytes are consumed
silently. All stdout writes go under the shared output mutex.

`input → feed` is the only inter-feed edge, so there is no cycle.

### `ipc` — the transport

Three pieces, no laterm imports, no external crate, no JSON parsing:

1. **`socket_path(cwd)`** — the §1.2 derivation; creates the parent directory as
   needed, shares `mangle_dir` with the transcript-directory derivation.
2. **Listener** — binds a `UnixListener`, removing a stale socket file first
   (`R-13.2`); accepts one connection at a time in arrival order (`R-2.5`), reads
   each to EOF as one raw payload, hands it to the caller over a channel. Watches
   the cancellation flag; removes the socket file on shutdown.
3. **`--hook` client** — `send(path, bytes)` and `forward(cwd, bytes)`: connect,
   write, exit. Any connect or write failure returns quietly so `main` exits 0
   (`R-2.4`). Writes nothing to stdout, ever (`R-11.2`).

`ipc` is transport-only, `hook` is parse-only, and they do not import each
other — `main` sequences them. Platform-split like `termbg`, implemented for unix
only (§11.1).

### `hook` — pure payload parser

`parse(bytes, watched_cwd) -> Option<Parsed { role, text, cwd }>` — full parse
with the `cwd` filter (`R-2.6`, `R-2.7`). `payload_cwd(bytes)` — minimal parse of
just `cwd` for the forwarder's socket derivation, with no event or content
validation, since the forwarder relays raw bytes regardless.

`role` is a parser-local enum, **not** `feed`'s `EntryStyle` — that mapping is
`main`'s, which is what keeps this module import-free. (`convo` returns a
different shape; the two parsers are neutral in the same *sense* but share no
types.) Uses `serde` + `serde_json`; never panics.

### `convo` — historical jsonl parser (catch-up only)

The only surviving jsonl-parse path. `parse(line) -> Option<ParsedEntry>` returns
the entry's RFC 3339 timestamp (which `main` needs for the `R-3.2` window)
alongside its text `Segment`s, each carrying a role string. Handles both the
array-of-blocks and plain-string `message.content` forms (`R-3.4`), and returns
`None` when an entry yields no segments — so `main` can index the first segment
for the entry's role without a bounds check.

Owns `is_jsonl(path)`, the pure path predicate for catch-up's directory scan. It
moved here from the deleted `watch` module, along with `extract` — the live-tail
entry point the hook pivot removed. Uses `serde_json`; never panics.

### `mathscan` — delimiter scanner

`scan(&str) -> Vec<Segment>`, where `Segment` is a struct
`{ kind: Kind, text: String, display: bool }` and `Kind` is `Text | Math`. For
`Text`, `text` is verbatim source and `display` is unused; for `Math`, `text` is
the inner expression, delimiters stripped and whitespace trimmed (`R-5.6`). Flat
and document-ordered — no anchor windowing, no neighbor-context logic. Empty vec
only for empty input.

### `render` — the RaTeX pipeline

```
ratex_parser::parse(expr)              -> Ast
ratex_layout::layout(&ast, &opts)      -> LBox
ratex_layout::to_display_list(&lbox)   -> DisplayList
ratex_render::render_to_png(&dl, &ro)  -> Vec<u8>
```

Returns `(png_bytes, height_px)` or `Err`, enforcing the `R-7.6` cap via
`MAX_WIDTH`/`MAX_HEIGHT` (4096). `set_theme` stores the two knobs `R-8.2`/`R-8.4`
need: glyph color via `LayoutOptions::with_color`, background via
`RenderOptions.background_color` (`Color { r, g, b, a }`, `a = 0.0` transparent).

RaTeX is synchronous and returns `Result` rather than panicking, so `R-7.5` needs
no `catch_unwind` and no timeout at this boundary. This module produces PNG bytes
and a height and knows nothing of the output protocol.

### `graphics` — protocol selection

`select() -> Option<Protocol>` in `R-8.1` order: kitty (env), imgcat (env), Sixel
(DA1 round-trip — last, because it costs a terminal query). Both env-based
encoders are private functions here; wire formats are SPEC.md §3.2. Uses
`base64`.

### `sixel` — Sixel encoder

`supported()` — DA1 via `termbg::query_terminal`, `parse_da1` looking for
attribute `4`. `set_background(r, g, b)` — the opaque compositing color, set once
after background detection (`R-8.3`). `detect_cell_height(timeout)` — a second
`query_terminal` (`ESC[16t`, `parse_cell_height`).

`encode(png, rows)` decodes to RGBA8 with the `png` crate, composites
partially-transparent pixels against the background color (the format carries no
alpha and would otherwise render them black), pads the height to a multiple of
`SIXEL_BAND` (6) and — when the cell height is known — of the cell height, then
emits DCS + 1:1 raster attributes, color registers on the `COLOR_SCALE` (100)
scale, and 6-row RLE bands based at `SIXEL_CHAR_BASE` (0x3F). The cell-multiple
padding stops Windows Terminal filling trailing dark cells.

`rows` is accepted and **ignored** (§11.2). Its only laterm import is
`termbg::query_terminal`.

### `termbg` — terminal I/O

`query(timeout) -> Option<(u8,u8,u8)>` (OSC 11; `parse_osc11` and `is_dark` are
platform-independent); `query_terminal(request, timeout)`, the `pub(crate)`
helper underneath both OSC 11 and `sixel`'s probes; `raw_input() ->
Option<RawInput>`, persistent raw input for `input`'s read loop, RAII-restoring
the prior mode on drop (`R-13.3`); `term_width() -> usize`, 80 on failure. Every
`unsafe` block carries a `// SAFETY:` rationale.

**unix** — `query_terminal` opens `/dev/tty`, not stdout, preserving `R-11.1`,
behind an RAII `RawModeGuard` (`cfmakeraw` on construct, prior termios restored
on drop, so a panic mid-I/O cannot strand the tty); `select(2)` for the timeout.
`raw_input` operates on fd 0: clears `ECHO | ICANON | IEXTEN`, keeps `OPOST` (so
`\n`→`\r\n` translation is unaffected) and `ISIG` (`R-4.6`), sets `VMIN=0 /
VTIME=1`. `term_width` via `ioctl(TIOCGWINSZ)`.

**Windows** — full parity, not a stub. `windows-sys` Console API: `GetStdHandle`,
`SetConsoleMode` with `ENABLE_VIRTUAL_TERMINAL_INPUT`, `WaitForSingleObject` for
the timeout, `ReadConsoleA` for the reply. The request goes out via
`WriteConsoleA` to the **console output handle**, mirroring the unix `/dev/tty`
write and keeping `R-11.1` intact. Handles validated against
`INVALID_HANDLE_VALUE`. `raw_input` operates on conin: clears
`ENABLE_LINE_INPUT | ENABLE_ECHO_INPUT`, sets `ENABLE_VIRTUAL_TERMINAL_INPUT`,
leaves output mode alone. `term_width` via `GetConsoleScreenBufferInfo`.

### `logging` — log configuration

Path from `main`'s `--log` only; append at mode 0600 (unix). Holds an exclusive
writer claim for the process lifetime (`R-12.4`): unix advisory
`flock(LOCK_EX | LOCK_NB)` via `libc`; Windows `share_mode(FILE_SHARE_READ)` via
std `OpenOptionsExt`, the constant taken from `windows-sys` rather than a
hand-rolled `0x1`. `FILE_SHARE_READ` — rather than no sharing — is what keeps
`tail -f` working on both platforms. Never writes stdout.

---

## 4. Dependency Direction

```
main
  |
  +---> ipc                   (UDS transport: listener + --hook client; std only)
  +---> hook                  (pure payload parser; serde_json)
  +---> convo                 (catch-up jsonl parse; serde_json)
  +---> feed ----> mathscan
  |          \---> render     (ratex-parser, ratex-layout, ratex-render, ratex-types)
  |          \---> graphics   (base64; kitty/imgcat encoders are private fns here)
  |          |          \--> sixel (png; calls termbg::query_terminal)
  |          \---> logging
  +---> input ---> feed
  |          \---> termbg
  +---> termbg
  +---> logging

(all modules may use logging)
```

Strictly downward, no cycles. `input → feed` is the only inter-feed edge.

---

## 5. Architectural Invariants

Design constraints of *this* implementation, distinct from SPEC.md's behavioral
requirements. Violating one is a blocking defect even when behavior looks
correct, because each is load-bearing for something else.

### 5.1 Isolation

`ipc`, `hook`, `convo`, and `mathscan` **import no other laterm module**,
depending only on `std` and at most one external crate. That is what makes them
fuzzable and unit-testable in isolation, and why the role→`EntryStyle` mapping
lives in `main` rather than `hook`. `ipc` and `hook` in particular do not import
each other: transport and parsing are separable, and `main` sequences them.

`sixel`'s call to `termbg::query_terminal` is the single permitted exception,
only because the DA1 and cell-size probes need the same raw-tty machinery OSC 11
does.

### 5.2 stdout writers

Only `main`, `feed`, and `input` write to `std::io::stdout` (`R-11.1`). Every
other module must not — including `termbg`, which writes its terminal requests to
`/dev/tty` or the console handle precisely to avoid it.

### 5.3 One mangling helper

The transcript-directory and socket-path derivations use the **same function**
(`ipc::mangle_dir`). Forking it — even into two implementations that agree
today — breaks `R-2.3` the moment one is fixed and the other is not. It must also
track Claude Code's convention byte for byte (SPEC.md §3.1); do not change it
unilaterally.

### 5.4 RaTeX confinement

No module other than `render` imports `ratex-parser`, `ratex-layout`,
`ratex-render`, or `ratex-types`. Swapping the math engine should be a change to
one file.

### 5.5 Protocol confinement

kitty and imgcat exist only as private functions in `graphics`; `sixel` is
imported only by `graphics`. Nothing outside `graphics` names a protocol —
callers go through `graphics::select` and `Protocol::encode`.

---

## 6. Data Flow

```
Claude Code (per turn) -- hook fires: laterm --hook <event>, stdin = payload JSON
  v
hook::payload_cwd  ->  ipc::forward: socket_path(cwd), connect, write raw bytes,
                       exit 0  (no listener -> exit 0 silently, nothing to stdout)
  v  raw JSON payload over the Unix-domain socket
ipc listener accept-loop (renderer)  ->  one raw payload per connection
  v
hook::parse(bytes, watched_cwd)  ->  Parsed { role, text, cwd }
  |  dropped on cwd mismatch / unknown event / empty content / bad JSON
  v
main maps role -> feed::EntryStyle
  v
feed::emit_entry(style, texts, &RenderCtx)      (under the output mutex)
  |  mathscan::scan -> Vec<Segment>, flat and document-ordered
  v
  per segment:
    Text  -> write verbatim
    Math  -> render::render -> Ok((png, height_px)) | Err
               Err -> re-delimited raw LaTeX, continue  (R-7.5)
               rows = max(1, round(height_px / ref_x_height * ROW_SCALE))
               height >= 1.5 * ref_x_height
                 ? block:  newline + proto.encode(png, rows) + newline
                 : inline: proto.encode(png, rows) in flow
  v
  bold open marker + tinted body + neutral images + bold close marker
    (inline, or own line at column 0) + color reset + blank line

catch-up:  *.jsonl -> convo::parse -> grouped per source entry -> one
           feed::emit_entry each, framing identical to the live path
paste:     input::PasteParser -> feed::emit_entry(PASTE_STYLE, ...)
```

---

## 7. Build and Toolchain

```sh
make build    # debug (cargo build)
make release  # optimized -> target/release/laterm
make test     # cargo test
make check    # type-check all three release targets (validates cfg flags)
make dist     # cross-build all three -> dist/ via cargo-zigbuild
make install  # copy this OS's dist binary to ~/.local/bin (INSTALL_DIR overrides)
make fmt      # cargo fmt
make lint     # cargo clippy --all-targets
make setup    # rustup targets + cargo-zigbuild (needs zig: brew install zig)
```

The Makefile is a thin wrapper over cargo — plain `cargo build`/`cargo test`
still work. Outputs go to `target/` and `dist/` only, never into `src/`.

**Cross-compilation.** `make dist` builds `aarch64-apple-darwin`,
`x86_64-unknown-linux-gnu`, and `x86_64-pc-windows-gnu` from one host via
`cargo-zigbuild`, using zig as the cross-linker — rustc bundles none, which is
what otherwise makes Mac→Linux/Windows impractical. It uses the `-gnu` Windows
triple because zigbuild cannot target `-msvc`; `-gnu` binaries run fine on
Windows. `.github/workflows/release.yml` instead builds each target **natively**
on its own OS runner on tags, so the *released* Windows binary is `-msvc`. Local
iteration across platforms vs. what ships.

**`make install`** goes to `~/.local/bin` (`INSTALL_DIR ?= $(HOME)/.local/bin`),
creating the directory if absent, removing the existing binary before copying
(fresh inode), and clearing the macOS quarantine attribute. The removal is not
cosmetic — see CONVENTIONS.md. The destination is load-bearing: `--install-hooks`
bakes `current_exe()` into settings as an absolute path (`R-10.2`), so hooks
installed from one location keep pointing there after the binary moves.

**Font embedding.** `ratex-render`'s `embed-fonts` feature bundles the KaTeX
fonts into the binary, satisfying `R-1.5`. No font directory exists at runtime.

---

## 8. RaTeX Integration

RaTeX ([github.com/erweixin/RaTeX](https://github.com/erweixin/RaTeX)) is a pure
Rust, KaTeX-compatible renderer, declared as four crates at v0.1.9. It was chosen
over `go-latex`, which the original Go implementation used and which failed on
`\begin`/`\end` environments (matrices, `aligned`, …), operator-limit stacking
(`\sum`, `\int` in display mode), accents (`\hat`, `\bar`, `\vec`, …), and
stretchy delimiters. RaTeX handles all four and imposes no command allowlist,
which is what makes `R-7.7` achievable rather than aspirational.

---

## 9. Dependency Justification

No dependency may be added without an entry here.

| Dependency | Module | Justification |
|---|---|---|
| `ratex-parser` v0.1.9 | `render` | KaTeX-compatible LaTeX parser; pure Rust, no external runtime |
| `ratex-layout` v0.1.9 | `render` | TeX box-model layout engine for the pipeline |
| `ratex-render` v0.1.9 (`embed-fonts`) | `render` | PNG renderer; `embed-fonts` bundles KaTeX fonts into the binary (`R-1.5`) |
| `ratex-types` v0.1.9 | `render` | Shared types required to wire the pipeline stages |
| `serde` v1 | `convo`, `hook` | Derive macros for JSON deserialization |
| `serde_json` v1 | `convo`, `hook`, `main` | Transcript lines, hook payloads, `settings.json` merge |
| `base64` v0.22 | `graphics` | PNG payload encoding for kitty and imgcat |
| `png` v0.17 | `sixel` | PNG→RGBA8 decode; the encoder itself is in-house (§1.4) |
| `chrono` v0.4 | `main` | RFC 3339 timestamp parsing for the `--catch-up` window (`R-3.2`) |
| `ctrlc` v3 | `main` | Cross-platform signal handling (`R-13.1`); MIT/Apache-2.0 |
| `libc` v0.2 | `termbg`, `logging` (unix, `[target.'cfg(unix)']`) | termios raw mode + `select(2)` for terminal queries; `ioctl(TIOCGWINSZ)` for width; `flock(LOCK_EX \| LOCK_NB)` for the log writer claim |
| `windows-sys` v0.59 | `termbg`, `logging` (windows, `[target.'cfg(windows)']`) | Console API (`GetStdHandle`, `SetConsoleMode`, `WaitForSingleObject`, `ReadConsoleA`, `WriteConsoleA`) for terminal queries; `GetConsoleScreenBufferInfo` for width; `INVALID_HANDLE_VALUE`; `FILE_SHARE_READ` (feature `Win32_Storage_FileSystem`) |

The UDS transport adds no crate (`std::os::unix::net`).

---

## 10. Testing Strategy

**Pure and deterministic — test hard, no terminal needed.** `mathscan`, `hook`,
`convo`, and `ipc::socket_path` import nothing of laterm and touch no I/O. They
carry the parsing correctness of `R-5`, `R-2.7`, and `R-3.4`, and are the
highest-value unit-test and fuzz surface in the crate. Fixture payloads worth
covering: `UserPromptSubmit` and `Stop`, `cwd` match and mismatch, unknown event,
empty content, malformed JSON. For `socket_path`, that the derivation is stable
for a given cwd — `R-2.3` in one assertion.

**Cheap integration checks.** The `--hook` client's two hardest guarantees are
both observable from a shell: exit 0 with no listener (`R-2.4`), and empty stdout
under all conditions (`R-11.2`).

**Low test value.** `graphics`, `sixel`, and `termbg` are dominated by terminal
round-trips and escape formatting. Their parsers (`parse_da1`, `parse_osc11`,
`parse_cell_height`) are the testable parts and are already separable; the
encoders are verified by looking at a terminal.

---

## 11. Implementation Status and Gaps

Shortfalls of *this implementation* against SPEC.md — distinct from SPEC.md §5,
which lists limitations no implementation is expected to overcome.

**11.1 Windows live path not implemented.** `ipc` is Unix-domain-socket only, so
`--hook`, `--install-hooks`, and live rendering do not work on Windows. The
module is platform-split in the shape `termbg` uses, with the Windows half
unimplemented. Paste, protocol selection (Sixel via DA1), and `termbg` are at
full Windows parity and are unaffected.

**11.2 Sixel ignores the proportional row count.** `sixel::encode` accepts `rows`
and discards it. kitty and imgcat scale to a cell count natively; Sixel has no
equivalent, so the image renders at its native pixel height, padded *up* to whole
sixel bands and (when the cell height was detected) whole character cells, never
scaled to a target row count. A partial shortfall against `R-7.2`: on a Sixel
terminal, math does not track the terminal's text size.
