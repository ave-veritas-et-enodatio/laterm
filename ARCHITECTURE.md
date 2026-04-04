# LaTerm Architecture Design

A Go PTY wrapper that intercepts LaTeX math expressions from child process output
and renders them as Sixel graphics or Unicode text.

This document is the authoritative design reference. It supersedes `INITIAL_SPEC.md`
where they conflict.

---

## 1. Invariants

These must hold regardless of implementation choices. Violating any invariant is a
blocking defect.

**Build and Deployment**

1. **Single static binary, no CGo.** `CGO_ENABLED=0`. No external runtime
   dependencies. Build-time Go module dependencies are permitted but must be
   justified in the dependency table. Binary name: `laterm`.

2. **Makefile as single build entry point.** Required targets: `build`, `test`
   (unit), `integration-test`. All build outputs go to `bin/` at the project
   root, `.gitignore`d. No build outputs scattered in the source tree.

**Data Integrity**

3. **Byte-transparent passthrough.** Every byte not part of a recognized math
   expression arrives at the user's terminal unmodified, in order, with no added
   latency. This includes ANSI escape sequences, UTF-8 multibyte characters, and
   arbitrary binary data.

4. **Ordered output.** Post-math text never appears before rendered math. The
   system buffers post-math output during render. Bounded buffer with degraded
   fallback (flush raw LaTeX) if the limit is exceeded.

5. **Single terminal data path.** One read loop from the child PTY, one write
   path to the user's terminal. The state machine sits inline in this path. Only
   the `stream` package writes to `os.Stdout`.

**Resilience**

6. **Graceful degradation chain.** Sixel render failure -> Unicode fallback.
   Unknown LaTeX macro -> raw LaTeX passthrough. State machine timeout or byte
   budget -> flush buffered bytes as literal text. Sanitizer rejection -> flush
   as literal text. No silent data loss at any point in the chain.

7. **Signal fidelity.** SIGINT, SIGTERM, SIGWINCH forwarded to child process.
   SIGWINCH updates PTY size and notifies renderer of new width. Works in both
   raw and cooked mode.

8. **Terminal state restoration.** When the wrapper enters raw mode, it must
   `defer` a restore of the original terminal state. Signal handlers for
   SIGTERM, SIGINT, SIGHUP, and SIGQUIT must restore terminal state before
   exiting. Panic recovery at the top of `main` must include terminal state
   restore. SIGKILL and OOM are documented as unrecoverable.

**Security**

9. **LaTeX input sanitization.** All LaTeX extracted by the state machine must
   pass through an allowlist sanitizer before reaching `go-latex`. The
   allowlist is an explicit set of ~80-120 known-safe LaTeX commands. Any
   command not on the allowlist causes the entire expression to be rejected and
   flushed as literal text. The sanitizer enforces a nesting depth budget.
   The sanitizer is stdlib-only; it does not import `go-latex` or any
   rendering package.

10. **Panic recovery at dependency boundaries.** Every call into `go-latex`
    and `go-sixel` must be wrapped in `recover()`. A panic from a dependency
    results in the expression being flushed as literal text and a warning log
    entry. The wrapper process must never crash due to a dependency panic.

11. **Rendering resource caps.** Parsing and rendering execute under a hard
    wall-clock timeout (goroutine + `select`, not trusting `go-latex`'s
    context support). Sixel image dimensions are capped to the terminal's
    pixel dimensions. Oversized images fall back to Unicode or literal text.

**State Machine**

12. **State machine isolation.** Pure function of `(current_state, input_byte)
    -> (next_state, output_action)` plus timer/byte-budget. No I/O, no
    knowledge of rendering, PTYs, or terminals. Testable with pure byte
    sequences.

13. **ANSI escape sequence awareness.** The state machine must track when input
    bytes are inside an ANSI/VT escape sequence (CSI, OSC, DCS, APC, PM, SOS,
    and ESC-initiated sequences). A `$` byte encountered inside an escape
    sequence must NOT trigger a math state transition. Escape sequence bytes
    pass through unmodified.

14. **False-positive mitigation.** Shell-variable heuristics reject `$PATH`,
    `$HOME`, `$(cmd)`, `${var}`, and similar patterns. Byte budget (512 bytes
    inline, 4096 bytes block) and time budget (200ms inline) enforce upper
    bounds. Thresholds are configurable.

**Observability**

15. **Structured, leveled logging.** Uses `log/slog` or a thin wrapper. Writes
    to file (mode 0600), optionally tees to stderr. Never writes to stdout.
    Level is runtime-configurable via `LATERM_LOG_LEVEL` env var or CLI flag.
    Raw child-process content appears only at debug/trace level.

16. **Runtime boundary validation.** Contract checks at PTY read/write, state
    machine transitions, renderer input, and sanitizer input. Expectation
    checks validate goroutine context where relevant. Violations are logged
    through the logging system, never panicked.

---

## 2. Module Skeleton

### Package Layout

```
laterm/
  cmd/laterm/              -- main: CLI args, startup, terminal restore, exit
  internal/
    pty/                   -- PTY lifecycle: spawn, raw/cooked, signal forwarding, resize
    stream/                -- Read loop, state machine integration, write coordination
    statemachine/          -- Pure delimiter detection, ANSI tracking, buffering, timeouts
    sanitize/              -- LaTeX allowlist validation, nesting depth, rejection
    render/                -- Renderer interface + selection logic
      unicode/             -- Unicode fallback renderer (stdlib only)
      sixel/               -- Sixel renderer (go-latex + go-sixel, timeout + recovery)
    termcap/               -- DA1 query, Sixel detection, terminal size in cells + pixels
    logging/               -- slog configuration, file + stderr output, level management
  Makefile
  go.mod
  go.sum
```

### Package Responsibilities and Constraints

**`cmd/laterm/`**
- Responsibility: Parse CLI args and env vars. Wire together all internal
  packages. Enter raw mode (if TTY). Set up terminal state restore (defer +
  signal handlers + panic recovery). Start the stream loop. Propagate child
  exit code.
- Imports: All `internal/` packages.
- Must NOT contain: Loop logic, state machine logic, rendering logic, or
  sanitization logic. This is wiring only.

**`internal/pty/`**
- Responsibility: Create PTY, spawn child process, manage raw/cooked mode
  transitions, forward signals (SIGINT, SIGTERM, SIGWINCH) to child, resize
  PTY on SIGWINCH, save and expose original terminal state for restoration.
- Imports: `github.com/creack/pty`, `golang.org/x/term`, `logging`.
- Must NOT import: `statemachine`, `render`, `stream`, `sanitize`.
- Exposes the PTY as `io.Reader`/`io.Writer` and the child `*os.Process`.
  Callers never see `creack/pty` types.

**`internal/stream/`**
- Responsibility: Single read loop from child PTY (`io.Reader`). Feeds bytes
  to the state machine. Dispatches math expressions through the sanitizer
  then to the renderer. Writes all output (passthrough text, rendered math,
  flushed literals) to a single `io.Writer` (the user's terminal). Manages
  the post-math output buffer and its overflow fallback.
- Imports: `statemachine`, `sanitize`, `render`, `logging`.
- Must NOT import: `pty`, `termcap`, `creack/pty`, `go-latex`, `go-sixel`.
- Owns the single write path to the user's terminal. No other package writes
  to stdout.

**`internal/statemachine/`**
- Responsibility: Pure byte-level state transitions for delimiter detection.
  Tracks six states: TEXT, ANSI_ESCAPE, POTENTIAL_MATH, INLINE_MATH,
  BLOCK_MATH, and sub-states for ANSI sequence types (CSI, OSC, DCS, APC,
  PM, SOS). Buffers potential math content. Enforces byte budget and reports
  time budget expiry when told by the caller. Implements shell-variable
  heuristic rejection.
- Imports: stdlib only (no external dependencies, no internal packages except
  possibly `logging` for contract-check violations).
- Must NOT import: `render`, `sanitize`, `pty`, `stream`, `termcap`.
- Must NOT perform I/O. Receives bytes, returns actions. The caller manages
  timers and I/O.
- Key design point: ANSI escape tracking is integral to the state machine,
  not a separate filter. When the machine enters an escape sequence, `$`
  bytes are inert.

**`internal/sanitize/`**
- Responsibility: Validate extracted LaTeX expressions against an explicit
  allowlist of known-safe commands before they reach `go-latex`. Enforce a
  nesting depth budget. Reject expressions containing any command not on the
  allowlist. Rejection means the entire expression is refused (not
  partially sanitized).
- Imports: stdlib only. Specifically: `strings`, `unicode`, `logging`.
- Must NOT import: `go-latex`, `render`, `statemachine`, `pty`, `stream`.
- The allowlist is a static data structure (map or set) defined in this
  package. It contains ~80-120 entries covering: Greek letters, operators,
  relation symbols, arrows, delimiters, accents, font commands, spacing,
  and common environments (e.g., `\frac`, `\sqrt`, `\sum`, `\int`,
  `\lim`, `\begin{matrix}`, `\end{matrix}`).
- The sanitizer is a pure function: `(expression string) -> (clean string,
  error)`. On rejection, the error describes which command was disallowed.
- Nesting depth budget: configurable, default 20. Expressions exceeding it
  are rejected.

**`internal/render/`**
- Responsibility: Define the `Renderer` interface. Implement renderer
  selection logic (Sixel vs. Unicode based on terminal capabilities).
- Imports: `termcap`, `logging`.
- Must NOT import: `pty`, `stream`, `statemachine`, `sanitize`.
- The `Renderer` interface: `Render(ctx context.Context, latex string,
  maxWidth int) ([]byte, error)`. Returns rendered bytes (Sixel escape
  sequence or UTF-8 text). The caller writes them.

**`internal/render/unicode/`**
- Responsibility: Convert LaTeX expressions to Unicode approximations using
  lookup tables. Greek letters, common operators, simple super/subscripts.
  Unknown macros pass through as raw LaTeX.
- Imports: stdlib only.
- Must NOT import: `go-latex`, `go-sixel`, `sixel/`, `render/` (parent).

**`internal/render/sixel/`**
- Responsibility: Parse LaTeX via `go-latex`, render to `image.RGBA`, encode
  to Sixel via `go-sixel`. Enforce wall-clock timeout around the entire
  parse-render-encode pipeline. Cap image dimensions to terminal pixel
  bounds. Wrap all `go-latex` and `go-sixel` calls in `recover()`.
  On any failure (timeout, panic, oversized, error) return an error so the
  caller can fall back.
- Imports: `codeberg.org/go-latex/latex`, `github.com/mattn/go-sixel`,
  `logging`.
- Must NOT import: `pty`, `stream`, `statemachine`, `sanitize`, `unicode/`.
- Timeout: the entire Render call runs in a goroutine; the caller selects
  on the result channel and a context deadline. If the goroutine outlives
  the deadline, the result is discarded. Default timeout: 5 seconds for
  block math, 2 seconds for inline.
- Panic recovery: a `defer recover()` inside the render goroutine catches
  panics from `go-latex` and `go-sixel`, converts them to errors.
- Image dimension cap: before Sixel encoding, check image bounds against
  terminal pixel dimensions from `termcap`. If either dimension exceeds
  the terminal, return an error (caller falls back to Unicode or literal).

**`internal/termcap/`**
- Responsibility: Query terminal capabilities. Send DA1 (`\x1b[c`) and
  parse response for Sixel support (attribute `4`). Query terminal size
  in cells (`TIOCGWINSZ`) and pixels. Provide current dimensions on demand
  (updated on SIGWINCH notification).
- Imports: `golang.org/x/term`, `logging`.
- Must NOT import: `pty`, `stream`, `statemachine`, `render`, `sanitize`.

**`internal/logging/`**
- Responsibility: Configure `log/slog` with structured output. Support file
  output (mode 0600) and optional stderr tee. Parse and apply log level from
  env/flag. Provide package-level access to the configured logger.
- Imports: stdlib only (`log/slog`, `os`, `io`).
- Must NOT import: any other internal package.
- Log file path configurable via `LATERM_LOG_FILE` env var or CLI flag.
  Default: no file (stderr only if enabled).

### Dependency Direction

```
cmd/laterm
  |
  +---> pty
  +---> stream ---> statemachine
  |       |    \--> sanitize
  |       |     \-> render (interface)
  |       |
  +---> render ---> render/unicode
  |       |    \--> render/sixel
  |       |
  +---> termcap
  +---> logging

All packages -----> logging (for contract checks and diagnostics)

External deps:
  pty        --> github.com/creack/pty, golang.org/x/term
  termcap    --> golang.org/x/term
  render/sixel --> codeberg.org/go-latex/latex, github.com/mattn/go-sixel
```

Dependency direction is strictly downward. No cycles. `statemachine` and
`sanitize` depend only on stdlib. The two external LaTeX/Sixel dependencies
are confined to `render/sixel/`. The PTY dependency is confined to
`internal/pty/`.

### Dependency Justification

| Dependency | Package | Justification |
|---|---|---|
| `github.com/creack/pty` | `internal/pty` | PTY creation and management. Go stdlib has no PTY support. Mature, widely used, pure Go. |
| `golang.org/x/term` | `internal/pty`, `internal/termcap` | Terminal raw mode, state save/restore, size queries. Extended stdlib maintained by Go team. |
| `codeberg.org/go-latex/latex` | `internal/render/sixel` | LaTeX parsing and rendering to image. Only known pure-Go LaTeX renderer. Confined behind sanitizer and timeout. |
| `github.com/mattn/go-sixel` | `internal/render/sixel` | Sixel encoding from `image.Image`. Non-trivial protocol implementation. Confined behind panic recovery. |

No other external dependencies are permitted without updating this table and
providing justification.

---

## 3. Acceptance Criteria

Observable behavioral outcomes that must be true when implementation is
complete. Organized by component, in implementation priority order.

### PTY Proxy (Priority 1)

- `laterm bash` behaves identically to `bash` for interactive use: arrow
  keys, tab completion, history navigation, Ctrl+C, Ctrl+D, Ctrl+Z all
  function correctly.
- `echo "hello" | laterm cat` works in cooked mode (stdin is not a TTY).
- Terminal resize is reflected in the child process (i.e., `stty size` in
  the child reports updated dimensions after the parent terminal is resized).
- SIGINT sent to `laterm` is forwarded to the child process.
- `laterm` exits with the same exit code as the child process.
- Non-math bytes from the child arrive at the user's terminal unmodified.
- If `laterm` is killed by SIGTERM, SIGINT, SIGHUP, or SIGQUIT while in
  raw mode, the user's terminal is restored to its original state.
- If a panic occurs in `main`, the terminal is restored before the process
  exits.
- SIGKILL and OOM are documented as unrecoverable; terminal state may be
  left corrupted (user runs `reset`).

### State Machine (Priority 2)

- `$\sigma$` is detected as inline math and the content `\sigma` is
  extracted.
- `$$\int_0^1 f(x) dx$$` is detected as block math, including when the
  expression spans multiple lines.
- `$PATH`, `$HOME`, `$(cmd)`, `${var}` are flushed as literal text (not
  treated as math).
- Inline byte budget (default 512 bytes) exceeded causes flush as literal.
- Block byte budget (default 4096 bytes) exceeded causes flush as literal.
- Inline time budget (default 200ms) exceeded causes flush as literal.
- Consecutive math expressions (e.g., `$a$ and $b$`) are detected and
  rendered independently.
- A `$` byte inside a CSI sequence (e.g., `\x1b[...$..$m`) does not
  trigger a math state transition.
- A `$` byte inside an OSC sequence (e.g., `\x1b]...$...\x07`) does not
  trigger a math state transition.
- A `$` byte inside DCS, APC, PM, and SOS sequences does not trigger a
  math state transition.
- ANSI escape sequences pass through to the terminal unmodified regardless
  of state machine state.
- The state machine has no I/O dependencies; it is fully testable with pure
  byte sequences and deterministic timer signals.

### Sanitizer (Priority 2, parallel with state machine)

- An expression containing only allowlisted commands (e.g.,
  `\frac{\alpha}{\beta}`) passes validation unchanged.
- An expression containing a non-allowlisted command (e.g., `\input{file}`,
  `\write`, `\catcode`, `\def`, `\newcommand`) is rejected entirely.
- Rejection means the caller receives an error; the expression is not
  partially passed through.
- Nesting depth exceeding the budget (default 20) causes rejection.
- The allowlist contains entries for: Greek letters (alpha through omega,
  upper and lower), common operators (`\frac`, `\sqrt`, `\sum`, `\prod`,
  `\int`, `\lim`, `\log`, `\sin`, `\cos`, `\tan`, etc.), relation symbols
  (`\leq`, `\geq`, `\neq`, `\approx`, `\equiv`, etc.), arrows
  (`\rightarrow`, `\leftarrow`, `\Rightarrow`, etc.), delimiters (`\left`,
  `\right`, `\big`, `\Big`, etc.), accents (`\hat`, `\bar`, `\tilde`,
  `\vec`, etc.), environments (`\begin`, `\end` with allowed environment
  names), and spacing commands (`\,`, `\;`, `\quad`, `\qquad`, etc.).
- The sanitizer has no dependency on `go-latex`. It operates on the raw
  LaTeX string using its own command extraction logic.
- Expressions with no backslash commands (e.g., `x + y = z`, `2^{10}`)
  pass validation (they contain no commands to reject).

### Unicode Renderer (Priority 3)

- Greek letters (`\alpha` through `\omega`, `\Gamma` through `\Omega`)
  render as their Unicode equivalents.
- Common operators render: `\int` -> `\u222B`, `\sum` -> `\u2211`,
  `\prod` -> `\u220F`, `\infty` -> `\u221E`, `\pm` -> `\u00B1`,
  `\times` -> `\u00D7`, `\div` -> `\u00F7`, `\partial` -> `\u2202`.
- Simple superscripts render: `^2` -> superscript 2, `^n` -> superscript n
  (where Unicode superscript exists).
- Simple subscripts render: `_i` -> subscript i, `_0` -> subscript 0
  (where Unicode subscript exists).
- Unknown macros are passed through as raw LaTeX text (e.g., `\obscure`
  appears as `\obscure` in the output).
- The Unicode renderer uses stdlib only (no external dependencies).

### Sixel Renderer (Priority 4)

- Output is a valid Sixel escape sequence (begins with `\x1bP`, ends with
  `\x1b\\`).
- Rendered image width does not exceed the terminal's pixel width.
- Rendered image height does not exceed the terminal's pixel height.
- If the computed image dimensions would exceed terminal pixel bounds, the
  renderer returns an error (caller falls back).
- Rendering completes within the wall-clock timeout (default 2s inline, 5s
  block). If it does not, the result is discarded and an error is returned.
- A panic in `go-latex` during parsing does not crash the process; it is
  recovered, logged, and results in fallback.
- A panic in `go-sixel` during encoding does not crash the process; same
  recovery behavior.
- DA1 capability detection correctly identifies Sixel support (attribute `4`
  in the response).
- When Sixel is not supported by the terminal, the system selects the
  Unicode renderer at startup without attempting Sixel rendering.

### Cross-Cutting

- Log output goes to a file (when configured) with mode 0600. Log file
  path is configurable via `LATERM_LOG_FILE`.
- Log level is configurable via `LATERM_LOG_LEVEL` env var. Supports at
  minimum: debug, info, warn, error.
- Raw child-process content (the actual bytes being processed) appears only
  at debug or trace level, never at info or above.
- Nothing appears on stdout except terminal data intended for the user's
  display.
- `make build` produces `bin/laterm`.
- `make test` runs unit tests.
- `make integration-test` runs integration tests.
- Runtime contract checks (invalid state transitions, nil arguments at
  boundaries, out-of-range values) are logged through the logging system,
  never cause a panic.
- Post-math output buffering has a bounded size. If the buffer fills during
  rendering, the system flushes the raw LaTeX and unblocks the output
  stream rather than blocking indefinitely or consuming unbounded memory.
- Fuzz tests exist for the state machine (byte sequences) and the sanitizer
  (LaTeX strings) to exercise panic paths and edge cases.

---

## 4. Data Flow Summary

This section describes the runtime data flow for reference. It is not
prescriptive about implementation details.

```
User's terminal (stdin)
     |
     v
  [laterm main]
     |
     +--> input bytes --> child PTY (stdin) --> child process
     |
     |    child process --> child PTY (stdout)
     |                          |
     |                          v
     |                    [stream: read loop]
     |                          |
     |                          v
     |                    [statemachine: byte-by-byte]
     |                     /         \
     |               passthrough    math expression extracted
     |                  |                    |
     |                  |                    v
     |                  |              [sanitize: allowlist check]
     |                  |               /              \
     |                  |          rejected           accepted
     |                  |           |                    |
     |                  |      flush literal             v
     |                  |           |              [render: Sixel or Unicode]
     |                  |           |               /              \
     |                  |           |          success            failure
     |                  |           |            |                  |
     |                  |           |       rendered bytes     flush literal
     |                  |           |            |                  |
     |                  v           v            v                  v
     |                [stream: single write path to stdout]
     |                          |
     v                          v
User's terminal (stdout) <------+
```

---

## 5. Known Limitations (Document in README)

- **Terminal escape passthrough**: A malicious child process could emit
  arbitrary terminal escape sequences. `laterm` passes all non-math
  terminal escapes through unmodified. This is accepted risk; filtering
  terminal escapes would break legitimate terminal functionality.

- **SIGKILL/OOM**: If `laterm` is killed by SIGKILL or terminated by OOM,
  the terminal may be left in raw mode. The user must run `reset` to
  recover. This is inherent to any program that modifies terminal state.

- **go-latex fidelity**: The sanitizer restricts LaTeX to a safe subset.
  Some valid LaTeX that uses advanced features (custom macros, package
  imports, catcode manipulation) will be rejected and displayed as literal
  text. This is a deliberate security/functionality tradeoff.

- **Unicode rendering fidelity**: Unicode approximation of math is lossy.
  Complex expressions (matrices, multi-level fractions) may not render
  readably. The Sixel path handles these; Unicode is a best-effort
  fallback.
