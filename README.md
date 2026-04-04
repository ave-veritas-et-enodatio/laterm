# LaTerm

A terminal wrapper that intercepts LaTeX math expressions from a child process and renders them as Sixel graphics or Unicode text, inline in your terminal output.

## Requirements and Supported Platforms

- Go 1.26 or later (build from source only)
- A Unix-like OS (Linux, macOS)
- A Sixel-capable terminal for high-fidelity rendering (e.g., iTerm2, WezTerm, foot, mlterm); any terminal works with Unicode fallback

## Quick Start Guide

Install the binary directly with Go:

```sh
go install github.com/benn-herrera/laterm/cmd/laterm@latest
```

Then wrap any command:

```sh
laterm claude
```

Math expressions in the output — delimited by `$...$` (inline) or `$$...$$` (block) — are rendered automatically. Everything else passes through unchanged.

## Usage

```
laterm <command> [args...]
```

Examples:

```sh
# Wrap an interactive session
laterm claude
laterm bash

# Works in non-interactive (piped) mode too
echo "The answer is $x^2 + 1$" | laterm cat
```

`laterm` exits with the same exit code as the child process. Signals (Ctrl+C, Ctrl+Z, window resize) are forwarded to the child.

When stdin is not a TTY (piped mode), `laterm` runs in cooked mode without raw terminal management. PTY and signal forwarding still apply; only raw-mode setup is skipped.

## How It Works

`laterm` spawns the target command inside a PTY (pseudo-terminal), acting as a transparent proxy between the child process and your terminal. A byte-level state machine scans the child's output stream for `$` and `$$` delimiters while tracking ANSI escape sequences — a `$` inside an escape sequence is never mistaken for a math delimiter.

When a math expression is extracted, it is validated against an allowlist of known-safe LaTeX commands before rendering. If the terminal supports Sixel graphics, the expression is parsed and rendered to an image, then encoded as a Sixel escape sequence. If Sixel is not supported, a Unicode approximation is produced using lookup tables. If rendering fails at any step, the raw LaTeX is passed through as literal text — no output is ever silently dropped.

Post-math text is buffered during rendering so output order is preserved.

## Configuration

| Variable | Values | Description |
|---|---|---|
| `LATERM_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | Log verbosity. Default: `info`. |
| `LATERM_LOG_FILE` | File path | Write logs to this file (mode 0600). Default: no file. |

Logs are never written to stdout. Raw child-process content is only logged at `debug` level.

## Known Limitations

**Terminal escape passthrough.** `laterm` forwards all non-math terminal escape sequences from the child process to your terminal unmodified. A misbehaving child process could emit arbitrary escape sequences. Filtering them would break legitimate terminal functionality, so this is accepted risk.

**SIGKILL and OOM.** If `laterm` is killed by SIGKILL or terminated by OOM, the terminal may be left in raw mode. Run `reset` to recover. This is inherent to any program that puts the terminal in raw mode.

**LaTeX subset only.** The sanitizer accepts a fixed set of LaTeX commands — Greek letters, common operators, fractions, square roots, subscripts, superscripts, accents, font commands (`\mathcal`, `\mathbb`, `\mathrm`, `\text`, `\operatorname`, etc.), and standard delimiters and arrows. Expressions using custom macros, `\begin`/`\end` environments, package imports, or advanced features like `\def` or `\catcode` are rejected and displayed as literal text. This is a deliberate security tradeoff. Some complex expressions that pass the sanitizer may fall back to Unicode rendering if the Sixel renderer cannot handle them; the `|` token in math mode is not yet supported.

**Unicode rendering fidelity.** Unicode math rendering is a best-effort approximation. Complex expressions — matrices, multi-level fractions, deeply nested structures — may not render readably in Unicode mode. Use a Sixel-capable terminal for full fidelity.

## Building from Source

Requires Go 1.26 or later.

```sh
git clone https://github.com/benn-herrera/laterm.git
cd laterm
make build
```

The binary is written to `bin/laterm`. Additional make targets:

| Target | Description |
|---|---|
| `make build` | Build `bin/laterm` |
| `make test` | Run unit tests |
| `make integration-test` | Run integration tests |
| `make dist` | Build a distribution binary to `dist/laterm-darwin-arm64` |
| `make dequarantine` | Remove macOS quarantine attribute from the dist binary |

## License

<!-- TODO: add license -->

## Third Party Acknowledgements

| Library | Author / Organization | License | Usage |
|---|---|---|---|
| [`github.com/creack/pty`](https://github.com/creack/pty) | Thomas Roccia (creack) | MIT | PTY creation and management |
| [`golang.org/x/term`](https://pkg.go.dev/golang.org/x/term) | Go Authors | BSD-3-Clause | Terminal raw mode, state save/restore, size queries |
| [`golang.org/x/sys/unix`](https://pkg.go.dev/golang.org/x/sys) | Go Authors | BSD-3-Clause | Terminal pixel dimension queries (TIOCGWINSZ) |
| [`github.com/mattn/go-sixel`](https://github.com/mattn/go-sixel) | Yasuhiro Matsumoto (mattn) | MIT | Sixel encoding from Go images |
| go-latex v0.2.0 (vendored) | go-latex contributors | BSD-3-Clause | LaTeX parsing and rendering to image; source vendored at `internal/golatex/` with in-repo fixes |
| [`codeberg.org/go-fonts/*`](https://codeberg.org/go-fonts) | go-latex contributors | OFL-1.1 | Font data consumed by the vendored go-latex font backend |
| [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image) | Go Authors | BSD-3-Clause | Image primitives used by the vendored go-latex rendering pipeline |
