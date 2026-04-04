# What Was Asked

Implement LaTerm from scratch — a Go PTY wrapper that intercepts LaTeX math expressions (`$...$` and `$$...$$`) from a child process's output stream and renders them as Sixel graphics or Unicode text, inline in the terminal. The deliverable was a single static binary with no runtime dependencies. Build-time Go module dependencies were permitted but had to be justified. Source material was an initial spec, which the design phase superseded.

---

# What Changed

## Design and architecture

The architect produced a complete design before any code was written: 16 binding invariants, a 10-package module skeleton, a dependency justification table, and acceptance criteria organized by component. A security review during this phase identified one critical issue — go-latex receiving unsanitized input — which was resolved by adding an `internal/sanitize/` package to the module skeleton. That package was not in the initial spec; it was introduced here. The revised design documented terminal state restoration across four fallback paths, a graceful degradation chain (Sixel → Unicode → literal passthrough), and the run() pattern for deferred cleanup.

## Implementation (four waves)

Code was written in four sequential waves to respect package dependencies:

- **Wave 1**: Makefile, go.mod, internal/logging, internal/statemachine, internal/sanitize, internal/render (interface and selection logic)
- **Wave 2**: internal/termcap, internal/render/unicode, internal/pty
- **Wave 3**: internal/stream (orchestration), internal/render/sixel
- **Wave 4**: cmd/laterm (entry point wiring)

The build passed with `go test -race -count=1 ./...` after all four waves.

## Tests

A dedicated test-writing phase added statemachine_test.go (30+ cases covering all acceptance criteria plus a fuzz test) and logging_test.go (12 cases covering initialization, env var parsing, file permission, and stdout isolation). Fuzz tests for sanitize and render/unicode were also written.

## Bug fixes from review

Two review iterations caught and fixed the following, in descending severity:

**Critical (found in iteration 1):**
- Race condition in stream's render lifecycle: goroutine-managed render results were coordinated through a raw channel with no synchronization guarantee; replaced with a WaitGroup pattern.
- Sixel renderer used terminal column count as a pixel width, producing absurd image dimensions; fixed to use actual pixel dimensions from termcap.

**Critical (found in iteration 2 / Phase 3b confirmation):**
- `os.Exit(exitCode)` at the bottom of main() bypassed all deferred cleanup, leaving the terminal in raw mode on normal exit. Refactored to the `run()` pattern: main() calls `os.Exit(run())`, all cleanup lives in `run()` as deferred calls.
- SIGINT and SIGTERM were swallowed after the child process exited. Fixed by calling `signal.Reset` on child death so subsequent signals reach the process with their default disposition.
- Double MakeRaw: the ground-truth terminal state was being saved after MakeRaw had already been called once, so restoration would restore raw mode rather than the original cooked state. Fixed to save terminal state before any terminal manipulation.
- Post-math buffer overflow caused silent data loss. Fixed so overflow flushes the raw LaTeX and unblocks the output stream rather than discarding bytes.
- Shutdown ordering was corrected so terminal restoration cannot race with active output writes.

**Warnings addressed across both iterations:**
- NewLoop now returns an error on invalid config rather than panicking.
- A semaphore caps concurrent render goroutines to prevent unbounded goroutine accumulation.
- Pre-decode dimension check in the Sixel renderer rejects images before attempting decode.
- ESC byte (0x1b) added to the sanitizer's control character rejection set.
- Each background goroutine has its own `defer recover()` that restores terminal state.
- Differentiated timeouts: 2 seconds for inline math, 5 seconds for block math.
- Expression length limit enforced in the sanitizer before allowlist traversal.
- Logging cleanup function properly managed.

## Documentation

AGENTS.md, README.md, and ARCHITECTURE.md were written and reviewed. The tech-writer-reviewer found gaps in the dependency table and import constraint documentation in ARCHITECTURE.md; both were addressed. The ARCHITECTURE.md is the authoritative design reference and supersedes the initial spec.

---

# Design Decisions

**`internal/sanitize/` was not in the initial spec.** The security review identified that go-latex is an external dependency receiving attacker-controlled input (anything a child process emits). The allowlist sanitizer was added as a hard boundary: only ~200 known-safe LaTeX commands reach go-latex. Rejection is all-or-nothing — no partial sanitization. This was the primary security mitigation.

**run() pattern over os.Exit in main().** This was a late-stage critical fix. The original implementation called `os.Exit(exitCode)` after cleanup at the bottom of main(). Go's os.Exit does not unwind the stack, so no deferred calls ran. The fix moves all cleanup into `run()` and has main() call only `os.Exit(run())`. This is documented as a gotcha in AGENTS.md because it is easy to reintroduce.

**Timeout as goroutine + select, not context.** go-latex does not reliably honor context cancellation. The Sixel renderer launches the parse-render-encode pipeline in a goroutine and selects on the result channel and a time.After. If the goroutine outlives the deadline, its result is discarded. This means a hung goroutine may outlive the render call, but the calling pipeline is never blocked.

**Shell-variable heuristic in the state machine.** `$PATH`, `$(cmd)`, `${var}` patterns are extremely common in terminal output (shell prompts, command echoing, script output) and would produce high false-positive math detection rates. The state machine implements lightweight heuristic rejection for these patterns. This is documented as an invariant (false-positive mitigation) rather than an optimization.

**ANSI escape tracking is integral to the state machine, not a pre-filter.** A `$` byte inside a CSI, OSC, DCS, APC, PM, or SOS sequence must not trigger math detection. The state machine has explicit states for each escape sequence type. A pre-filter approach was considered and rejected because it would require buffering escape sequences separately, complicating the single write path invariant.

**Five justified external dependencies.** The dependency policy allows build-time dependencies with justification. All five are listed in ARCHITECTURE.md section 2 with explicit rationale. `go-latex` and `go-sixel` are confined exclusively to `internal/render/sixel/`. `creack/pty` is confined to `internal/pty/`. `golang.org/x/term` and `golang.org/x/sys/unix` are extended stdlib maintained by the Go team.

---

# Security Findings

One critical and five warning-level findings were identified in the security review (Phase 1a). The critical finding — go-latex receiving unsanitized terminal output — drove the addition of `internal/sanitize/`. The five warnings were:

- **Terminal escape passthrough**: accepted risk; filtering would break legitimate terminal functionality. Documented in README and ARCHITECTURE.
- **`$` in ANSI sequences**: mitigated by ANSI-aware state machine states.
- **Panic propagation from go-latex/go-sixel**: mitigated by `recover()` wrappers in the Sixel renderer goroutine, with expression flushed as literal text on panic.
- **Raw mode restoration**: mitigated by the four-path terminal restoration scheme.
- **Sixel image dimension exhaustion**: mitigated by pre-decode dimension cap against terminal pixel bounds.
- **Log file path disclosure**: log file path is set by the operator via `LATERM_LOG_FILE`; the file is created at mode 0600. Acknowledged and documented.

---

# What to Review

**License field in README.md** — line 97 contains `<!-- TODO: add license -->`. This must be resolved before any external distribution. The repository has no LICENSE file.

**go-latex filesystem I/O audit** — go-latex loads font data at render time. Whether this is unconditional filesystem I/O or bundled data was not verified during implementation. The allowlist, timeout, and panic recovery mitigate the risk, but a one-time audit of go-latex's font loading behavior would provide additional assurance, especially for sandboxed environments.

**`closingHandler` in internal/logging/logging.go** — the type wraps cleanup state but its `Close()` method is never called from any caller. Cleanup flows through the returned closure instead. This is dead code and should be simplified or removed before the code is considered production-ready.

**Sleep-based synchronization in stream tests** — several tests in internal/stream/stream_test.go use `time.Sleep` for goroutine synchronization. These are inherently flaky under load or on slow CI runners. Review `stream_test.go` and replace sleep-based waits with deterministic synchronization (channels, WaitGroups, or polling with timeout).

**Integration tests do not exist** — `make integration-test` builds and runs tests with `-tags integration`, but no files with `//go:build integration` have been written. The target succeeds vacuously. PTY-level integration tests that exercise the full pipeline with a real child process are the highest-confidence test category for a PTY proxy and should be written before production use.

**Extended fuzz runs** — fuzz tests exist for statemachine (`FuzzFeed`), sanitize (`FuzzCheck`), and render/unicode (`FuzzRender`). The implementation session ran each for seconds only. Before production use, run each for hours and add any found corpus entries to the repository.

---

# Unresolved Items

- No LICENSE file exists in the repository. The binary cannot be distributed without one.
- No integration test files exist despite the Makefile target being present.
