# LaTerm

A Claude Code sidecar that watches the active conversation log for the current
project and renders LaTeX math expressions as inline images in a separate
terminal window.

LaTerm does not wrap or intercept Claude Code. It runs alongside it: you launch
it in its own graphics-capable terminal window, and it tails the project's
conversation logs, echoing the conversation text with any math it contains
rendered as inline images in place.

![screenshot](screenshot.png)

## Requirements and Supported Platforms

- Rust toolchain (build from source) or a pre-built binary for your platform
- macOS, Linux, or Windows
- A terminal that supports an inline-image protocol:
  - **kitty graphics protocol** — kitty, ghostty (preferred)
  - **iTerm2 imgcat (OSC 1337)** — iTerm2, WezTerm
  - **Sixel** — Windows Terminal (v1.22+), xterm, foot, mlterm, WezTerm, and others
- Claude Code, run from the same project directory

LaTerm has full feature parity on Windows, including OSC 11 background-color
contrast detection. Windows Terminal (v1.22+) is supported via Sixel. WezTerm
on Windows supports imgcat.

A terminal supporting none of the three protocols is rejected at startup. There
is no Unicode text fallback.

## Quick Start

### Run from dist

```bash
# pick the binary for your platform
./dist/laterm-laterm-aarch64-apple-darwin
./dist/laterm-x86_64-pc-windows-gnu.exe
./dist/laterm-x86_64-unknown-linux-gnu
```

on Mac: `make dequarantine` first

### Build from source

```bash
git clone https://github.com/ave-veritas-et-enodatio/laterm.git
cd laterm
make release
```

The binary is written to `target/release/laterm`. Copy or symlink it onto your
`PATH`.

Open a second terminal window (kitty, ghostty, iTerm2, WezTerm, Windows Terminal,
or any Sixel-capable terminal), `cd` to the
same project directory where you run Claude Code, and start:

```sh
cd [your-project-dir-where-claude-is-used]
laterm [options]
```

As Claude Code's conversation produces LaTeX math —
`$...$` / `\(...\)` (inline) or `$$...$$` / `\[...\]` (display) — LaTerm
echoes the conversation text in its window, rendering each expression as an
image in place. You can also paste text (e.g. a snippet containing math) directly
into the LaTerm window; it is rendered like a conversation entry. Typed input is
not accepted — ordinary keystrokes produce a beep. Pressing Enter/Return inserts a separator — a blank line, then a terminal-width
rule of `═` (U+2550) characters in bold yellow, then a newline — to manually
divide topics in the rendered feed; pressing Enter again without any rendered
content in between just beeps (rules do not stack). Each rendered entry is
prefixed with a color-coded role marker: `(u)>` (bold green) for your prompts,
`[a]>` (bold cyan) for assistant replies, and `{p}>` (bold magenta) for pasted
text, making the feed easy to scan at a glance.

## Usage / Options

```
laterm [--log <PATH>] [--catch-up[=<MINS>]] [--help]
```

| Flag | Description |
|---|---|
| `--log <PATH>` | Write diagnostics to PATH. No logging unless this is given. |
| `--catch-up[=<MINS>]` | Before tailing, replay math from the last MINS minutes of conversation history (bare flag = 5 minutes). |
| `--help`, `-h` | Print usage and exit. |

## How It Works

1. From the working directory, LaTerm derives the Claude Code log directory:
   `~/.claude/projects/<cwd with '/' replaced by '-'>`.
2. It polls that directory (~500 ms) and tails every `*.jsonl` conversation log.
   Tail-only: content present at startup is not replayed. Pass `--catch-up` to
   first replay math from recent history before the tail begins.
3. For each new entry it extracts the text from user/assistant messages and
   scans it for LaTeX math.
4. Each expression is rendered to a PNG by the embedded RaTeX engine and emitted
   using the terminal's image protocol (kitty, imgcat, or Sixel — in that
   preference order). If rendering fails, the raw LaTeX is passed through as text.

Output is a **full echo**: the conversation text is mirrored verbatim, with each
math expression rendered as an image in place. Each entry is prefixed with a
color-coded role marker — `(u)>` for user entries, `[a]>` for assistant entries,
`{p}>` for pasted text — and followed by a blank-line separator. A small expression (single symbol,
simple sub/superscript) renders inline in the text flow; a tall one (fraction,
integral, summation) renders on its own line. Image size scales proportionally to
the terminal's text, so math sits naturally alongside the prose. At startup
LaTerm queries the terminal background (OSC 11) and
renders glyphs in a contrasting color on a transparent background, falling back
to black-on-white if the query is unanswered. (The Sixel path renders on an
opaque background of the detected terminal color, since Sixel transparency is
less universally honored.)

## Configuration

| Variable | Values | Description |
|---|---|---|
| `LATERM_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | Log verbosity when `--log` is given. Default: `info`. |

No logging by default — pass `--log <PATH>` to enable it. Both `--log <PATH>`
and `--log=<PATH>` are accepted; a missing argument is a usage error. When
enabled, the file is opened for append at mode 0600 (unix). If a second laterm
instance targets the same path, it cannot acquire the exclusive writer lock:
it prints one warning to stderr and continues running without logging (you can
still `tail -f` the live log from a reader). Only fatal pre-exit messages go to
stderr regardless. Raw conversation content is only logged at `debug` level.

Multiple instances sharing one default log file was the reason logging became
opt-in: nobody needs logs unless something is wrong, at which point you relaunch
with `--log`.

## Rendering

Math is rendered by [RaTeX](https://github.com/erweixin/RaTeX), a pure-Rust,
KaTeX-compatible math renderer (MIT license). Pipeline: `parse → layout →
display list → render_to_png`. KaTeX fonts are embedded in the binary (the
`embed-fonts` feature) — no external font directory or runtime is required.

RaTeX covers the full KaTeX syntax set: matrices, environments, stretchy
delimiters, operator-limit stacking (limits above/below), accents (`\hat`,
`\bar`, `\vec`, …), `\begin`/`\end` environments, and more. Expressions that
RaTeX cannot parse are passed through as literal text rather than silently
dropped. There is no command allowlist — RaTeX returns errors rather than
panicking, so bad input degrades gracefully.

## Known Limitations

- **Graphics-protocol terminals only.** Requires kitty graphics (kitty,
  ghostty), iTerm2 imgcat (iTerm2, WezTerm), or Sixel (Windows Terminal v1.22+,
  xterm, foot, mlterm, WezTerm, and others). Selection order: kitty → imgcat →
  Sixel. No Unicode fallback; a terminal supporting none is rejected at startup.
- **Polling latency.** The watcher polls at ~500 ms, so a rendered expression
  may appear up to that long after it is written.
- **Background detection is best-effort.** Glyph contrast relies on an OSC 11
  query; terminals that do not answer (within 200 ms) get a black-on-white
  fallback rather than theme-matched glyphs.

## Building from Source

Requires a Rust toolchain (stable).

```sh
git clone https://github.com/ave-veritas-et-enodatio/laterm.git
cd laterm
make setup    # install rustup targets (once)
make build    # debug build
make release  # optimized build → target/release/laterm
make test     # run unit tests
make dist     # cross-build all three release targets into dist/
```

Plain `cargo build --release` and `cargo test` work too. The root Makefile is
a thin wrapper over cargo; the Go prototype keeps its own Makefile under
`prototype/` and is not part of the Rust build.

## License

<!-- TODO: add license -->

## Third Party Acknowledgements

| Library | Author / Organization | License | Usage |
|---|---|---|---|
| [`ratex-parser`](https://github.com/erweixin/RaTeX) v0.1.9 | erweixin | MIT | KaTeX-compatible LaTeX parser |
| [`ratex-layout`](https://github.com/erweixin/RaTeX) v0.1.9 | erweixin | MIT | TeX box-model layout engine |
| [`ratex-render`](https://github.com/erweixin/RaTeX) v0.1.9 (`embed-fonts`) | erweixin | MIT | PNG renderer; `embed-fonts` feature bundles KaTeX fonts into the binary |
| [`ratex-types`](https://github.com/erweixin/RaTeX) v0.1.9 | erweixin | MIT | Shared type definitions for the RaTeX crate family |
| [`serde`](https://github.com/serde-rs/serde) v1 | David Tolnay, Erick Tryzelaar | MIT / Apache-2.0 | Derive macros for JSON deserialization |
| [`serde_json`](https://github.com/serde-rs/json) v1 | David Tolnay, Erick Tryzelaar | MIT / Apache-2.0 | Parsing `.jsonl` conversation log entries |
| [`base64`](https://github.com/marshallpierce/rust-base64) v0.22 | Marshall Pierce | MIT / Apache-2.0 | Encoding PNG bytes for kitty and imgcat image protocols |
| [`png`](https://github.com/image-rs/image-png) v0.17 | image-rs contributors | MIT / Apache-2.0 | PNG→RGBA decode for the Sixel encoder |
| [`chrono`](https://github.com/chronotope/chrono) v0.4 | Chrono Contributors | MIT / Apache-2.0 | RFC3339 timestamp parsing for `--catch-up` window filtering |
| [`ctrlc`](https://github.com/Detegr/rust-ctrlc) v3 | Antti Ker&#228;nen | MIT / Apache-2.0 | Cross-platform SIGINT/SIGTERM handler |
| [`libc`](https://github.com/rust-lang/libc) v0.2 | The Rust Project Developers | MIT / Apache-2.0 | Unix-only: termios raw mode and `select(2)` for OSC 11 background query |
| [`windows-sys`](https://github.com/microsoft/windows-rs) v0.59 | Microsoft | MIT / Apache-2.0 | Windows-only: Console API for OSC 11 background query |
