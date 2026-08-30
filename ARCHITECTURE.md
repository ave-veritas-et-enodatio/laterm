# ARCHITECTURE.md — LaTerm

**How this implementation satisfies [SPEC.md](SPEC.md).**

SPEC.md defines what LaTerm must do; this document defines the Rust program that
does it. Everything here is a choice — module boundaries, crates, transport,
build tooling — and could be made differently without changing what LaTerm is.

References of the form `R-n.m` cite SPEC.md requirements. **This document does
not restate requirements.** Where a mechanism exists to satisfy one, it cites it.
If you find behavior specified here and not in SPEC.md, that is a filing error to
correct, not a second source of truth.

---

## 1. Design Decisions

The choices that shaped everything else, with the reasoning that produced them.

### 1.1 Hook push over transcript tailing

**Decision:** live turns arrive as host-agent hook invocations over a
Unix-domain socket (`R-2.1`, `R-2.2`).

The original design tailed the `*.jsonl` transcript. That worked until Claude
Code moved to writing the transcript asynchronously (observed v2.1.220): the file
lags in-memory state, so a tailer misses or trails the turn it is supposed to be
showing live. Tailing is retained only for `--catch-up` (`R-3`), where the file
is already complete and the lag is irrelevant.

**Rejected alternative — OpenTelemetry log events.** Claude Code can emit turn
content as OTEL logs. Rejected on two counts: the exporter truncates event bodies
at 60 KB by default, which is a dealbreaker for exactly the long, math-dense
answers LaTerm exists to render; and it requires standing up an OTLP receiver, a
heavyweight dependency for a single-binary sidecar.

The cost of hooks is SPEC.md §5.1 — `Stop` carries only the final assistant
message, so pre-tool-call prose is not seen live. Accepted.

### 1.2 Unix-domain socket as the transport

A UDS costs no external crate (`std::os::unix::net`), is filesystem-addressable
(so the rendezvous of `R-2.3` reduces to deriving a path both sides compute the
same way), is inherently local, and carries filesystem permissions. A TCP port
would need allocation, discovery, and a story for why it is not listening on a
network.

The rendezvous path derivation is `$XDG_RUNTIME_DIR` (else `~/.cache/laterm/`) +
the dash-mangled project directory + `.sock`. **The mangling is the same helper
that derives the transcript directory** (`ipc::mangle_dir`), not a copy of it —
see §5.3.

This transport is unix-only; Windows is unsupported (§11.1).

### 1.3 Runtime reference-X sizing

`R-7.2`/`R-7.3` require image size and inline-vs-block layout to track the
terminal's text size. Rather than hard-code pixel thresholds, the renderer
measures its own output once: it renders a capital `X` at startup and expresses
both the size formula and the block threshold as multiples of that height. The
numbers then track RaTeX's actual metrics, and follow them through a version
bump, for free. Fallback when the reference render fails: **42 px**.

### 1.4 In-house Sixel encoder

`sixel.rs` encodes Sixel directly rather than pulling a Sixel crate plus a color
quantizer. The format is simple enough that the encoder is smaller than the
dependency surface it replaces, and it lets the compositing behavior of
`R-8.3`/§11.2 be handled exactly. Palette reduction is a bit-dropping loop that
caps at 256 registers — adequate for glyph rendering, which is near-monochrome.

### 1.5 One `Protocol` handle, selected once

`R-8.5` forbids repeating a terminal round-trip per image. `graphics::select`
returns a `Protocol` carrying an `encode_fn` pointer; that single value is wrapped
in an `Arc` and shared by catch-up, the listener loop, and the paste thread. No
call site names a specific protocol — `feed` calls `ctx.proto.encode(png, rows)`
and does not know which one it got.

---

## 2. Crate Layout

```
src/
  main.rs      wiring: mode dispatch, derivation, listener loop, signals
  feed.rs      output/render orchestration: markers, tint, images, RenderCtx
  input.rs     manual paste-only stdin: PasteParser, separator rule, BEL
  ipc.rs       UDS transport: path derivation, listener, --hook client
  hook.rs      pure hook-payload parser -> (role, text) + cwd
  convo.rs     historical jsonl parser (catch-up only) + is_jsonl()
  mathscan.rs  LaTeX delimiter scanner -> flat Vec<Segment>
  render.rs    RaTeX pipeline -> (png_bytes, height_px)
  graphics.rs  protocol selection + kitty/imgcat encoders (private fns)
  sixel.rs     Sixel encoder; supported() via DA1
  termbg.rs    OSC 11 / DA1 / cell-size queries; raw input; terminal width
  logging.rs   log configuration, file + stderr, level management
```

There are no `kitty.rs` / `imgcat.rs` files. Both encoders are private functions
inside `graphics.rs` (`kitty_supported`/`kitty_encode`,
`imgcat_supported`/`imgcat_encode`): each is a `supported()` predicate over
environment variables plus a formatter, with no state and no dependency of its
own. `sixel` is a module because it is an order of magnitude larger and needs
`termbg` for its probes.

`feed` and `input` are the output-feed modules; with `main` they are the only
modules permitted to write stdout (§5.2).

---

## 3. Module Responsibilities

### `main` — wiring only

Three modes, dispatched from CLI flags before any other work.

**1. `--hook <event>`** (`R-2.2`) — forward and exit. Read stdin, call
`hook::payload_cwd` for a minimal parse of just `cwd`, call `ipc::forward(cwd,
bytes)`, exit 0. Deliberately does *none* of the renderer's startup: no protocol
select, no background query, no theme, no `RenderCtx`, no paste thread. Silent on
failure (`R-2.4`) and silent on success (`R-11.2`).

**2. `--install-hooks [scope]`** (`R-10`) — merge and exit. Reads the target
`settings.json` with `serde_json`, merges the two entries additively under the
top-level `hooks` key, writes it back. The command string is `current_exe()` +
` --hook <event>` (`R-10.2`).

**3. normal** — the renderer. In order:

1. Parse flags. On `-C`/`--cwd`, `set_current_dir(PATH)` **first** (`R-9.2`).
   This is the footgun-free form: every later derivation reads `current_dir()`,
   so it mangles the OS-canonical absolute path and the transcript directory and
   socket path match by construction. No manual canonicalization anywhere.
2. Initialize logging (`R-12`).
3. `graphics::select` once; exit with an error if `None` (`R-8.1`).
4. `apply_theme` — `main`'s own function: query the background with
   `termbg::query`, pick a contrasting glyph color, and hand both to
   `render::set_theme` (`R-8.2`, `R-8.4`). When the selected protocol is sixel it
   also sets `sixel::set_background` and calls
   `sixel::detect_cell_height` (`R-8.3`).
5. Derive the transcript directory and, via `ipc`, the socket path — both from
   the possibly-changed working directory, through one shared mangling helper.
   Protocol selection and theme come first deliberately: neither reads the
   working directory, so nothing before this point can observe a stale one.
6. Print the startup line, and the missing-directory warning if applicable
   (`R-9.6`, `R-9.7`).
7. Register the `ctrlc` handler, which sets the shared cancellation flag
   (`R-13.1`).
8. Build the `feed::RenderCtx` once — its constructor performs the reference-X
   render (§1.3) and carries the protocol.
9. Spawn the stdin reader thread (`input::read_input`, given the output mutex,
   the `RenderCtx`, and the cancellation flag).
10. If `--catch-up`: `convo::parse` over the transcript files, grouped by source
    entry, each group through one `feed::emit_entry` (`R-3.5`).
11. Run the `ipc` listener loop. Per payload: `hook::parse` → drop on `cwd`
    mismatch (`R-2.6`) → map the parser's role enum to `feed`'s `EntryStyle` →
    `feed::emit_entry` under the output mutex. On exit, remove the socket file.

`main` holds no rendering, parsing, marker, protocol, or transport logic. The
role→`EntryStyle` mapping in step 10 lives here specifically so `hook` can stay
free of laterm imports (§5.1).

### `feed` — output and render orchestration

Owns the `EntryStyle` role markers (`USER_STYLE`/`ASSISTANT_STYLE`/`PASTE_STYLE`
— the concrete strings and colors of `R-6.2`), `role_style`, the sizing constants
(`ROW_SCALE` = 1.25, the 1.5× block threshold, `rows_for`), the `RenderCtx`, and
the `emit_entry`/`emit_segments` pipeline.

`emit_entry(style, texts, &RenderCtx)` walks each text's segment list in order:
`Text` written verbatim, `Math` through `render::render` then
`ctx.proto.encode(png, rows)`. `RenderCtx` collapses what were three separate
parameters (block threshold, reference height, protocol) into one value passed by
reference. An `at_line_start` cursor flag is threaded through `emit_segments` and
back out; it is what decides close-marker placement (`R-6.3`), and it is set by
both a block image and any text ending in a newline. `emit_entry` returns the
number of math segments emitted, which `main` uses for the catch-up log line.

Writes stdout **under the caller-held output mutex** — it takes no inner stdout
lock, because the mutex is the cross-thread serializer between the listener loop
and the paste thread. `emit_entry` clears the manual-separator armed state;
`arm_separator()` lets `input` re-arm and test it (`R-4.5`) without a `feed →
input` edge.

Imports `mathscan`, `render`, `graphics`, `logging`. Must not import `input`,
`ipc`, `hook`, `convo`, `termbg`.

### `input` — manual paste-only stdin

Holds a `termbg::raw_input()` guard for its lifetime. Enables bracketed paste
(`ESC[?2004h`) at startup, disables it (`ESC[?2004l`) on exit (`R-13.3`).

`PasteParser` is a byte-at-a-time state machine capturing between `ESC[200~` and
`ESC[201~`; the captured text goes through `feed::emit_entry` with `PASTE_STYLE`,
so a paste is one entry regardless of line count (`R-4.1`). Enter writes the
separator rule (`R-4.4`, width from `termbg::term_width()`); printable keystrokes
produce the throttled BEL (`R-4.3`); escapes and control bytes are consumed
silently.

All of its stdout writes go under the shared output mutex.

Imports `feed`, `termbg`, `logging`. `input → feed` is the only inter-feed edge,
so there is no cycle.

### `ipc` — the transport

Three pieces, no laterm imports, no external crate, no JSON parsing:

1. **`socket_path(cwd)`** — the §1.2 derivation. Creates the parent directory as
   needed. Shares `mangle_dir` with the transcript-directory derivation.
2. **Listener** — binds a `UnixListener`, removing a stale socket file first
   (`R-13.2`). Accepts one connection at a time in arrival order (`R-2.5`), reads
   each to EOF as one raw payload, hands it to the caller. Observes the
   cancellation flag; removes the socket file on shutdown.
3. **`--hook` client** — `send(path, bytes)` and `forward(cwd, bytes)`. Connect,
   write, exit. Any connect or write failure returns quietly so `main` can exit 0
   (`R-2.4`). Writes nothing to stdout, ever (`R-11.2`).

`ipc` is transport-only and `hook` is parse-only; they do not import each other.
`main` sequences them.

Platform-split like `termbg`, and implemented for unix only:
`std::os::unix::net` (§11.1).

### `hook` — pure payload parser

Parses a hook payload into `Parsed { role, text, cwd }`. `role` is a
parser-local enum, **not** `feed`'s `EntryStyle` — that mapping is `main`'s,
which is what keeps this module import-free. (`convo` returns a different shape,
`ParsedEntry`, carrying a timestamp and per-segment role strings; the two
parsers are neutral in the same *sense*, but their types are not shared.)

- `parse(bytes, watched_cwd)` — full parse with the `cwd` filter (`R-2.6`,
  `R-2.7`).
- `payload_cwd(bytes)` — minimal parse of just `cwd`, for the forwarder's socket
  derivation. No event or content validation: the forwarder relays raw bytes
  regardless of what they contain.

Uses `serde` + `serde_json`. Never panics.

### `convo` — historical jsonl parser (catch-up only)

The only surviving jsonl-parse path; not on the live path.
`parse(line) -> Option<ParsedEntry>` returns the entry's RFC 3339 timestamp
(which `main` needs for the `R-3.2` window) alongside its text `Segment`s, each
carrying a role string. Handles both the array-of-blocks and plain-string
`message.content` forms per `R-3.4`, and returns `None` when an entry yields no
segments — so `main` can index the first segment for the entry's role without a
bounds check. Owns `is_jsonl(path)`, the pure path predicate used by catch-up's
directory scan (it moved here from the deleted `watch` module, along with
`extract`, the live-tail entry point the hook pivot removed).

Uses `serde_json`. Never panics. No laterm imports.

### `mathscan` — delimiter scanner

`scan(&str) -> Vec<Segment>`, where `Segment` is a struct
`{ kind: Kind, text: String, display: bool }` and `Kind` is `Text | Math`. For
`Text`, `text` is verbatim source and `display` is unused; for `Math`, `text` is
the inner expression with delimiters stripped and whitespace trimmed (`R-5.6`).
Flat and document-order — no anchor windowing, no neighbor-context logic.
Returns an empty vec only for empty input.

No laterm imports.

### `render` — the RaTeX pipeline

```
ratex_parser::parse(expr)              -> Ast
ratex_layout::layout(&ast, &opts)      -> LBox
ratex_layout::to_display_list(&lbox)   -> DisplayList
ratex_render::render_to_png(&dl, &ro)  -> Vec<u8>
```

Returns `(png_bytes, height_px)` or `Err`. Enforces the `R-7.6` cap via
`MAX_WIDTH`/`MAX_HEIGHT` (4096). Glyph color via `LayoutOptions::with_color`;
background via `RenderOptions.background_color` (`Color { r, g, b, a }`, `a = 0.0`
transparent) — the two knobs `R-8.2`/`R-8.4` need.

RaTeX is synchronous and returns `Result` rather than panicking, so `R-7.5` needs
no `catch_unwind` and no timeout at this boundary.

The only module importing the RaTeX crates. Must not import `ipc`, `hook`,
`convo`, `mathscan`, `graphics`, `feed`, or `input` — it produces PNG bytes and a
height, and knows nothing of the output protocol.

### `graphics` — protocol selection

`select() -> Option<Protocol>` in the `R-8.1` order: kitty (env), imgcat (env),
Sixel (DA1 round-trip, last, because it costs a terminal query). Both env-based
encoders are private functions here; the wire formats are SPEC.md §3.2.

Imports `base64` and `sixel`. Must not import `render`, `mathscan`, `convo`,
`ipc`, `hook`, `feed`, or `input`.

### `sixel` — Sixel encoder

`supported()` — DA1 via `termbg::query_terminal`, `parse_da1` looks for attribute
`4`. `set_background(r, g, b)` — the opaque compositing color, set once after
background detection (`R-8.3`). `detect_cell_height(timeout)` — a second
`query_terminal` (`ESC[16t`, `parse_cell_height`).

`encode(png, rows)` decodes to RGBA8 with the `png` crate, composites
partially-transparent pixels against the background color (the Sixel format
carries no alpha and would otherwise render them black), pads the height to a
multiple of `SIXEL_BAND` (6) and — when the cell height is known — to a multiple
of the cell height, then emits the stream: DCS + 1:1 raster attributes, color
registers on the `COLOR_SCALE` (100) scale, 6-row RLE bands based at
`SIXEL_CHAR_BASE` (0x3F). The cell-multiple padding stops Windows Terminal
filling trailing dark cells.

`rows` is accepted and **ignored** — see §11.2.

Its only laterm import is `termbg::query_terminal`.

### `termbg` — terminal I/O

- `query(timeout) -> Option<(u8,u8,u8)>` — OSC 11; `parse_osc11` and `is_dark`
  are platform-independent.
- `query_terminal(request, timeout) -> Option<String>` — `pub(crate)` helper
  underneath both the OSC 11 query and `sixel`'s DA1/cell probes.
- `raw_input() -> Option<RawInput>` — persistent raw input for `input`'s read
  loop. RAII: prior mode restored on drop (`R-13.3`).
- `term_width() -> usize` — 80 on failure.

Every `unsafe` block carries a `// SAFETY:` rationale.

**unix** — `query_terminal` opens `/dev/tty` (not stdout — preserving `R-11.1`)
behind an RAII `RawModeGuard` (`cfmakeraw` on construct, prior termios restored
on drop, so a panic mid-I/O cannot strand the tty); `select(2)` for the timeout.
`raw_input` operates on fd 0: clears `ECHO | ICANON | IEXTEN`, keeps `OPOST` (so
`\n`→`\r\n` translation is unaffected) and `ISIG` (`R-4.6`), sets `VMIN=0 /
VTIME=1`. `term_width` via `ioctl(TIOCGWINSZ)`.

**Windows** — `windows-sys` Console API, full parity, not a stub. `GetStdHandle`,
`SetConsoleMode` with `ENABLE_VIRTUAL_TERMINAL_INPUT`, `WaitForSingleObject` for
the timeout, `ReadConsoleA` for the reply. The request is written with
`WriteConsoleA` to the **console output handle**, mirroring the unix `/dev/tty`
write and keeping `R-11.1` intact. Handles validated against `windows-sys`'
`INVALID_HANDLE_VALUE`. `raw_input` operates on conin: clears `ENABLE_LINE_INPUT |
ENABLE_ECHO_INPUT`, sets `ENABLE_VIRTUAL_TERMINAL_INPUT`, leaves output mode
alone. `term_width` via `GetConsoleScreenBufferInfo`.

No laterm imports.

### `logging` — log configuration

Path from `main`'s `--log` only. Append at mode 0600 (unix). Holds an exclusive
writer claim for the process lifetime (`R-12.4`): unix advisory `flock(LOCK_EX |
LOCK_NB)` via `libc`; Windows `share_mode(FILE_SHARE_READ)` via std
`OpenOptionsExt`, with the constant taken from `windows-sys` rather than a
hand-rolled `0x1`. `FILE_SHARE_READ` — rather than no sharing — is what keeps
`tail -f` working on both platforms.

Never writes stdout.

---

## 4. Dependency Direction

```
main
  |
  +---> ipc                   (UDS transport: listener + --hook client; std only)
  +---> hook                  (pure payload parser -> (role, text); serde_json)
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

Design constraints of *this* implementation, distinct from the behavioral
requirements in SPEC.md. Violating one is a blocking defect even when behavior
appears correct, because each is load-bearing for something else.

### 5.1 Isolation

`ipc`, `hook`, `convo`, and `mathscan` **import no other laterm module.** Each
depends only on `std` and, where noted, one external crate. This is what makes
them fuzzable and unit-testable in isolation, and it is why the role→`EntryStyle`
mapping lives in `main` rather than in `hook`.

`ipc` and `hook` in particular do not import each other: transport and parsing
are separable concerns and `main` sequences them.

`sixel`'s call to `termbg::query_terminal` is the single permitted exception, and
only because the DA1 and cell-size probes need the same raw-tty machinery OSC 11
does.

### 5.2 stdout writers

Only `main`, `feed`, and `input` write to `std::io::stdout` (`R-11.1`). `ipc`,
`hook`, `convo`, `mathscan`, `render`, `graphics`, `sixel`, `termbg`, and
`logging` must not — including `termbg`, which writes its terminal requests to
`/dev/tty` or the console handle precisely to avoid it.

### 5.3 One mangling helper

The transcript-directory derivation and the socket-path derivation use the **same
function** (`ipc::mangle_dir`). Forking it — even into two implementations that
agree today — breaks `R-2.3` the moment one is fixed and the other is not. It
must also track Claude Code's convention byte for byte (SPEC.md §3.1); do not
change it unilaterally.

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
Claude Code (per turn)
  |  hook fires: laterm --hook <event>, stdin = payload JSON
  v
[hook::payload_cwd]  minimal parse of cwd
  v
[ipc::forward]       socket_path(cwd); connect; write raw bytes; exit 0
  |                  (no listener -> exit 0 silently, nothing to stdout)
  |  raw JSON payload over the Unix-domain socket
  v
[ipc listener accept-loop]        (renderer process)
  |  one raw payload per connection, arrival order
  v
[hook::parse(bytes, watched_cwd)]
  |  (role, text)  |  drop on cwd mismatch / unknown event / empty / bad JSON
  v
[main: role -> feed::EntryStyle]
  v
[feed::emit_entry(style, texts, &RenderCtx)]      (under the output mutex)
  |
  |  mathscan::scan -> Vec<Segment>, flat and document-ordered
  v
  for each segment:
    Text  -> write verbatim
    Math  -> render::render -> Result<(png, height_px), _>
               |  Err -> pass raw LaTeX through, delimited, continue
               v
             rows = max(1, round(height_px / ref_x_height * ROW_SCALE))
             height >= 1.5 * ref_x_height
               ? block:  newline + proto.encode(png, rows) + newline
               : inline: proto.encode(png, rows) in flow
  v
  bold open marker + tinted body + neutral images
    + bold close marker (inline, or own line if at column 0)
    + color reset + blank line

catch-up:  *.jsonl -> convo::parse -> grouped per source entry
           -> one feed::emit_entry each  (framing identical to live)
paste:     input::PasteParser -> feed::emit_entry(PASTE_STYLE, ...)
```

---

## 7. Build and Toolchain

### 7.1 Targets

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

The Makefile is a thin wrapper over cargo — plain `cargo build` / `cargo test`
still work. Build outputs go to `target/` and `dist/` only, never into `src/`.

### 7.2 Cross-compilation

`make dist` builds `aarch64-apple-darwin`, `x86_64-unknown-linux-gnu`, and
`x86_64-pc-windows-gnu` from a single host via `cargo-zigbuild`, using zig as the
cross-linker — rustc bundles no cross-linker, which is what makes
Mac→Linux/Windows otherwise impractical. It uses the `-gnu` Windows triple
because zigbuild cannot target `-msvc`; `-gnu` binaries run fine on Windows.

`.github/workflows/release.yml` instead builds each target **natively** on its
own OS runner on tags, so the *released* Windows binary is `-msvc`. The two paths
exist for different purposes: `make dist` for local iteration across platforms,
CI for what ships.

### 7.3 `make install`

Installs to `~/.local/bin` (`INSTALL_DIR ?= $(HOME)/.local/bin`, overridable),
creating the directory if it does not exist. It removes the existing binary
before copying (fresh inode) and clears the quarantine attribute on macOS. The
removal is not cosmetic — see AGENTS.md.

The destination matters beyond convenience: `--install-hooks` bakes
`current_exe()` into the settings file as an absolute path (`R-10.2`), so hooks
installed from a binary at one location keep pointing there after the binary
moves.

### 7.4 Font embedding

`ratex-render`'s `embed-fonts` feature bundles the KaTeX fonts into the binary,
satisfying `R-1.5`. No font directory exists at runtime.

---

## 8. RaTeX Integration

RaTeX ([github.com/erweixin/RaTeX](https://github.com/erweixin/RaTeX)) is a pure
Rust, KaTeX-compatible renderer, declared as four crates at v0.1.9.

It was chosen over `go-latex`, which the original Go implementation used and
which failed on:

- `\begin`/`\end` environments (matrices, `aligned`, …);
- operator-limit stacking (`\sum`, `\int` limits above/below in display mode);
- accents (`\hat`, `\bar`, `\vec`, `\tilde`, …);
- stretchy delimiters.

RaTeX handles all four and imposes no command allowlist — which is what makes
`R-7.7` achievable rather than aspirational. It returns `Result` rather than
panicking, so the `R-7.5` fallback needs no panic machinery.

---

## 9. Dependency Justification

No dependency may be added without an entry here.

| Dependency | Module | Justification |
|---|---|---|
| `ratex-parser` v0.1.9 | `render` | KaTeX-compatible LaTeX parser; pure Rust, no external runtime |
| `ratex-layout` v0.1.9 | `render` | TeX box-model layout engine for the RaTeX pipeline |
| `ratex-render` v0.1.9 (`embed-fonts`) | `render` | PNG renderer; `embed-fonts` bundles KaTeX fonts into the binary (`R-1.5`) |
| `ratex-types` v0.1.9 | `render` | Shared types required to wire the RaTeX pipeline stages |
| `serde` v1 | `convo`, `hook` | Derive macros for JSON deserialization |
| `serde_json` v1 | `convo`, `hook`, `main` | Transcript lines (`convo`), hook payloads (`hook`), `settings.json` merge (`main`) |
| `base64` v0.22 | `graphics` | PNG payload encoding for the kitty and imgcat protocols |
| `png` v0.17 | `sixel` | PNG→RGBA8 decode for the Sixel encoder; the encoder itself is in-house (§1.4) |
| `chrono` v0.4 | `main` | RFC 3339 timestamp parsing for the `--catch-up` window (`R-3.2`) |
| `ctrlc` v3 | `main` | Cross-platform SIGINT/SIGTERM handling (`R-13.1`); MIT/Apache-2.0 |
| `libc` v0.2 | `termbg`, `logging` (unix only, `[target.'cfg(unix)']`) | termios raw mode + `select(2)` for terminal queries; `ioctl(TIOCGWINSZ)` for width; `flock(LOCK_EX \| LOCK_NB)` for the log writer claim |
| `windows-sys` v0.59 | `termbg`, `logging` (Windows only, `[target.'cfg(windows)']`) | Console API (`GetStdHandle`, `SetConsoleMode`, `WaitForSingleObject`, `ReadConsoleA`, `WriteConsoleA`) for the terminal queries; `GetConsoleScreenBufferInfo` for width; `INVALID_HANDLE_VALUE`; `FILE_SHARE_READ` (feature `Win32_Storage_FileSystem`) |

The UDS transport adds no crate (`std::os::unix::net`).

---

## 10. Testing Strategy

Where confidence is cheapest, given this decomposition.

**Pure and deterministic — test hard, no terminal needed.** `mathscan`, `hook`,
`convo`, and `ipc::socket_path` import nothing of laterm and touch no I/O. They
carry the parsing correctness of `R-5`, `R-2.7`, and `R-3.4`, and they are the
highest-value unit-test and fuzz surface in the crate.

Fixture payloads worth covering: `UserPromptSubmit` and `Stop`, `cwd` match and
mismatch, unknown event, empty content, malformed JSON. For `socket_path`, that
the derivation is stable for a given cwd — that is `R-2.3` in one assertion.

**Cheap integration checks.** The `--hook` client's two hardest guarantees are
both observable from a shell: exit 0 with no listener (`R-2.4`), and empty stdout
under all conditions (`R-11.2`).

**Low test value.** `graphics`, `sixel`, and `termbg` are dominated by terminal
round-trips and escape-sequence formatting. Their parsers (`parse_da1`,
`parse_osc11`, `parse_cell_height`) are the testable parts and are already
separable; the encoders are verified by looking at a terminal.

---

## 11. Implementation Status and Gaps

Known shortfalls of *this implementation* against SPEC.md. Distinct from SPEC.md
§5, which lists limitations no implementation is expected to overcome.

### 11.1 Windows live path not implemented

`ipc` is Unix-domain-socket only. On Windows, `--hook`, `--install-hooks`, and
live rendering do not work. The module is platform-split in the shape `termbg`
uses, with the Windows half unimplemented.

Paste, graphics protocol selection (Sixel via DA1), and `termbg` are at full
parity on Windows and are unaffected.

### 11.2 Sixel ignores the proportional row count

`sixel::encode` accepts `rows` and discards it. kitty and imgcat scale to a cell
count natively; Sixel has no equivalent, so the image renders at its native pixel
height, padded *up* to a whole number of sixel bands and (when the cell height was
detected) a whole number of character cells. It is never scaled down or up to a
target row count.

This is a partial shortfall against `R-7.2`: on a Sixel terminal, math does not
track the terminal's text size.
