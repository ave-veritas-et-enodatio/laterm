# AGENTS.md — LaTerm

Guidance for agents (automated or human) working on this codebase.
**Read ARCHITECTURE.md first.** It is the authoritative design reference and
supersedes any inference you draw from code alone.

---

## Project Overview

LaTerm is a Go PTY wrapper that intercepts LaTeX math expressions from child
process output and renders them as Sixel graphics or Unicode text. Every byte
that is not part of a recognized math expression passes through unmodified.

Module path: `github.com/benn-herrera/laterm`

---

## Build and Run

```sh
make build            # produces bin/laterm (CGO_ENABLED=0)
make test             # unit tests, all packages
make integration-test # integration tests (build tag: integration)
make clean            # removes bin/
make lint             # go vet ./...
make fmt              # gofmt -w .
```

Build outputs go to `bin/` only — never in the source tree. The binary name
is `laterm`.

To run after building:

```sh
bin/laterm bash
bin/laterm python3
```

---

## Package Structure

```
cmd/laterm/          CLI entry point. Wires all internal packages.
internal/
  pty/               PTY lifecycle: spawn, raw mode, signal forwarding, resize.
  stream/            Read loop, state machine integration, single write path.
  statemachine/      Pure delimiter detection, ANSI tracking, byte/time budgets.
  sanitize/          LaTeX allowlist validation, nesting depth enforcement.
  render/            Renderer interface and capability-based selection.
    unicode/         Unicode fallback renderer. Stdlib only.
    sixel/           Sixel renderer. go-latex + go-sixel. Timeout + recovery.
  termcap/           DA1 capability query, terminal size in cells and pixels.
  logging/           slog configuration, file + stderr output, level management.
```

### Package responsibilities in brief

**`cmd/laterm/`** — Wiring only. Parse args and env vars, initialize logging,
save terminal state, probe terminal capabilities, instantiate and connect all
internal packages, handle SIGHUP/SIGQUIT, start stream loop, propagate child
exit code. Contains no loop logic, no state machine logic, no rendering logic.

**`internal/pty/`** — Creates the PTY, spawns the child, manages raw/cooked
mode, forwards SIGINT/SIGTERM/SIGWINCH to child, resizes PTY on SIGWINCH.
Exposes the PTY as `io.Reader`/`io.Writer`. Callers never see `creack/pty`
types.

**`internal/stream/`** — Single read loop from the child PTY. Feeds bytes to
the state machine. On math extraction, calls sanitizer then renderer. Writes
all output (passthrough, rendered math, flushed literals) through a single
`io.Writer`. Manages the post-math output buffer and its overflow fallback.

**`internal/statemachine/`** — Pure function: `(state, byte) -> (next_state,
action)`. Seven states: TEXT, ANSI_ESCAPE, POTENTIAL_MATH,
POTENTIAL_UPPER_MATH, INLINE_MATH, BLOCK_MATH, and ANSI sub-states (CSI, OSC,
DCS, APC, PM, SOS). Enforces byte budget and reports time budget expiry.
Shell-variable heuristic rejection (`$PATH`, `$(cmd)`, `${var}`) with
two-byte lookahead for uppercase: `$X` followed by another letter → shell
variable; `$X` followed by `_`, `^`, `\`, `{`, digit, operator → math.

**`internal/sanitize/`** — Validates extracted LaTeX against an explicit
allowlist of ~80–120 known-safe commands before it reaches go-latex. Enforces
nesting depth budget (default 20). Rejection is all-or-nothing: on any unknown
command the entire expression is refused. Pure function: `(string) -> (string,
error)`.

**`internal/render/`** — Defines the `Renderer` interface:
`Render(ctx, latex string, maxWidth int) ([]byte, error)`. Implements
capability-based selection (Sixel vs. Unicode) and `FallbackRenderer` which
cascades: tries the primary renderer (Sixel), falls back to the secondary
(Unicode) on error or empty result.

**`internal/render/unicode/`** — Lookup-table conversion of LaTeX to Unicode
approximations. Handles Greek letters, operators, super/subscripts (with
recursive rendering of `\command` in subscript position), font-style commands
(`\mathcal{M}` → `M`, `\mathrm`, `\mathbb`, etc.), `\frac`, `\sqrt`,
accents, and ~140 command mappings. Unknown macros pass through as raw LaTeX.
No external dependencies.

**`internal/render/sixel/`** — Parses LaTeX via go-latex, renders to
`image.RGBA`, encodes to Sixel via go-sixel. Enforces wall-clock timeout
(goroutine + select). Caps image dimensions to terminal pixel bounds. All
go-latex and go-sixel calls wrapped in `recover()`.

**`internal/termcap/`** — Sends DA1 (`\x1b[c`) and parses the response to
detect Sixel support (attribute `4`). Queries terminal size in cells and
pixels via TIOCGWINSZ.

**`internal/logging/`** — Configures `log/slog`. File output at mode 0600,
optional stderr tee. Level controlled by `LATERM_LOG_LEVEL`. Log file path
controlled by `LATERM_LOG_FILE`.

---

## Key Architecture Constraints

These are invariants from ARCHITECTURE.md. Violating any is a blocking defect.

### Isolation constraints

- **`statemachine` and `sanitize` are stdlib-only.** No I/O, no external
  packages, no imports from any other internal package except possibly
  `logging` for contract-check violations.
- **Only `stream` writes to stdout.** No other package may write to
  `os.Stdout`. Logging goes to file or stderr.
- **External deps confined to `render/sixel/`.** `codeberg.org/go-latex/latex`
  and `github.com/mattn/go-sixel` are imported only in `internal/render/sixel/`.
  No other package imports them.
- **`pty` dependency confined to `internal/pty/`.** No other package imports
  `github.com/creack/pty`.

### Dependency direction

```
cmd/laterm
  |
  +---> pty
  +---> stream ---> statemachine
  |       |    \--> sanitize
  |       |     \-> render (interface)
  +---> render ---> render/unicode
  |       |    \--> render/sixel
  +---> termcap
  +---> logging

All packages -----> logging
```

No cycles. Direction is strictly downward.

### State machine

- Pure function. No I/O. No knowledge of rendering, PTYs, or terminals.
- ANSI escape tracking is integral to the state machine, not a pre-filter.
  A `$` byte inside any escape sequence (CSI, OSC, DCS, APC, PM, SOS, or
  ESC-initiated) is inert and must not trigger a math state transition.
- Receives bytes, returns actions. The caller (stream) manages timers and I/O.

### Panic recovery at dependency boundaries

Every call into go-latex and go-sixel must be wrapped in `recover()`. A panic
from a dependency results in the expression being flushed as literal text and
a warning log entry. The wrapper process must never crash due to a dependency
panic.

### Terminal state restoration

The original terminal state is saved at the top of `run()` (not in `main()`)
before any package touches the terminal. It is restored via multiple fallback
paths:

1. `defer term.Restore(...)` in `run()` — normal exit
2. `defer recover()` block in `run()` — panic in main goroutine
3. SIGHUP/SIGQUIT signal handler goroutine — controlling terminal gone or quit
4. `recover()` defers in each goroutine — panics in background goroutines

`session.RestoreTerminal()` is idempotent; it is safe to call from multiple
paths.

### Graceful degradation chain

Sixel render failure → Unicode fallback → raw LaTeX passthrough. The
`FallbackRenderer` in `render/render.go` implements the Sixel → Unicode
cascade automatically. No silent data loss at any stage.

### Post-math buffer

`stream` buffers output that arrives after a math expression is extracted but
before rendering completes. This buffer is bounded. If it fills, the system
flushes the raw LaTeX and unblocks the output rather than blocking or
consuming unbounded memory.

---

## Testing

```sh
make test             # runs go test ./...
make integration-test # runs go test -tags integration ./...
```

`make test` omits `-race` to keep the build fast. Use `-race` for manual runs
and CI verification:

```sh
go test -race -count=1 ./...
```

Fuzz tests exist in three packages:

| Package | Function |
|---|---|
| `internal/statemachine/` | `FuzzFeed` |
| `internal/sanitize/` | `FuzzCheck` |
| `internal/render/unicode/` | `FuzzRender` |

To run a fuzz target:

```sh
go test -fuzz=FuzzFeed ./internal/statemachine/
go test -fuzz=FuzzCheck ./internal/sanitize/
go test -fuzz=FuzzRender ./internal/render/unicode/
```

Integration tests use the `integration` build tag. They exercise the PTY proxy
and full data path end-to-end.

---

## Dependency Policy

**Zero delivery dependencies.** The shipped binary has no runtime
dependencies. Build-time Go module dependencies must be justified in the
dependency table in ARCHITECTURE.md section 2. Do not add a new dependency
without adding a row to that table with a justification.

Current direct dependencies:

| Dependency | Used in |
|---|---|
| `github.com/creack/pty` | `internal/pty/` |
| `golang.org/x/term` | `internal/pty/`, `internal/termcap/` |
| `codeberg.org/go-latex/latex` | `internal/render/sixel/` |
| `github.com/mattn/go-sixel` | `internal/render/sixel/` |
| `golang.org/x/sys/unix` | `internal/termcap/` |

Transitive indirect dependencies are tracked in `go.sum` and must not be
imported directly.

---

## Logging

- **Never write to stdout.** Logging goes to a file (when configured) or
  stderr. stdout is reserved exclusively for terminal data written by `stream`.
- Log file is created at mode 0600. Path set via `LATERM_LOG_FILE` env var.
- Level set via `LATERM_LOG_LEVEL` (debug, info, warn, error). Default: info.
- Raw child-process content (the actual bytes being processed) appears only at
  debug level, never at info or above.
- Runtime contract-check violations are logged through the logging system,
  never panicked.

---

## Common Gotchas

### `os.Exit` bypasses defers

`main()` calls `os.Exit(run())`. Terminal state restoration lives in `run()`
as deferred calls. If you add cleanup logic, put it in `run()` — not in
`main()` and not in an `init()`. A `defer` in `main()` will never run because
`os.Exit` does not unwind the stack.

### Signal handler goroutines need their own `recover()`

Each goroutine spun up in `run()` has its own `defer recover()` that calls
`session.RestoreTerminal()` before the goroutine exits. If you add a new
goroutine, follow the same pattern. An unrecovered panic in a goroutine kills
the entire process without running any defers in other goroutines, leaving the
terminal in raw mode.

### SIGHUP and SIGQUIT re-raise with default handler

The SIGHUP/SIGQUIT handler calls `signal.Reset(sig)` before re-raising via
`syscall.Kill`. This ensures the process exits with the correct signal status
(as seen by the parent shell). Do not replace this with `os.Exit`.

### SIGKILL and OOM are unrecoverable

There is no signal handler for SIGKILL. If the process is killed by SIGKILL or
OOM, the terminal is left in raw mode. The user must run `reset`. Document this
in user-facing materials but do not attempt to handle it in code.

### State machine receives bytes, not runes

The state machine operates on raw bytes. Do not pass decoded runes. UTF-8
multibyte sequences are handled transparently because the state machine only
acts on `$` (0x24) and escape-sequence bytes; all other bytes pass through
regardless of their role in a multibyte sequence.

### Renderer interface contract

`Render` returns `([]byte, error)`. On any failure — timeout, panic, oversized
image, unsupported expression — it returns an error. The caller (`stream`) is
responsible for fallback. A renderer must never return partial output alongside
an error.

### Sanitizer is all-or-nothing

The sanitizer either returns the original expression string unchanged or
returns an error. It does not return a modified or trimmed expression. If you
extend the sanitizer, preserve this contract.
