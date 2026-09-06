# LaTerm

A Claude Code sidecar that renders the active conversation's LaTeX math
expressions as inline images in a separate terminal window.

LaTerm does not wrap or intercept Claude Code. It runs alongside it: you launch
it in its own graphics-capable terminal window. Once Claude Code is configured
with LaTerm's hooks (`laterm --install-hooks`), it pushes each conversation turn
to the window over a local socket, and LaTerm echoes the turn text with any math
it contains rendered as inline images in place.

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
./dist/laterm-aarch64-apple-darwin
./dist/laterm-x86_64-pc-windows-gnu.exe
./dist/laterm-x86_64-unknown-linux-gnu
```

on Mac: `make dequarantine` first

### Build from source

```bash
git clone https://github.com/ave-veritas-et-enodatio/laterm.git
cd laterm
make dist       # cross-build the per-platform binaries into dist/
make install    # copy the right one to ~/.local/bin/laterm
```

`make install` selects the binary for your OS from `dist/`, copies it to
`~/.local/bin/laterm` (creating the directory if needed; override with
`INSTALL_DIR=...`), and on macOS removes the
quarantine attribute. It replaces any existing copy by removing it first (a
fresh inode), which avoids a macOS code-signing cache quirk that otherwise
`Killed:9`s an overwritten binary. Run `make dist` first so `dist/` is current.

Alternatively, `make release` writes a single `target/release/laterm`; copy or
symlink that onto your `PATH` yourself.

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
bracketed by a matched pair of color-coded role markers — opening `(u)>` / closing
`<(u)` (bold green) for your prompts, `[a]>` / `<[a]` (bold cyan) for assistant
replies, `{p}>` / `<{p}` (bold magenta) for pasted text — and the body text is
tinted in the role's color (green / cyan / magenta) for at-a-glance scanning.
Both markers and body tint are readable in light and dark themes.

## Usage / Options

```
laterm [-C <PATH>] [--log <PATH>] [--catch-up[=<MINS>]] [--version] [--help]
laterm --install-hooks [--project|--project-local|--global]   # one-time: configure Claude Code
laterm --hook <UserPromptSubmit|Stop>          # invoked BY Claude Code, not you
```

| Flag | Description |
|---|---|
| `--install-hooks [--project\|--project-local\|--global]` | Write LaTerm's two hook entries into Claude Code's `settings.json` (`--project` → `.claude/settings.json` (shared/committed); `--project-local` → `.claude/settings.local.json` (personal/untracked); `--global` → `~/.claude/settings.json`; default `--project-local`), then exit. At most one target flag. Run this once so the live path works. |
| `--hook <event>` | Internal: invoked by Claude Code per turn (`UserPromptSubmit`/`Stop`). Forwards the hook's stdin JSON to the running window and exits. You do not run this yourself. |
| `--cwd <PATH>`, `-C` | Derive the watched log directory (and rendezvous socket) from PATH instead of the process working directory. Both `--cwd <PATH>`/`--cwd=<PATH>` and `-C <PATH>` are accepted; a missing argument is a usage error. |
| `--log <PATH>` | Write diagnostics to PATH. No logging unless this is given. |
| `--catch-up[=<MINS>]` | Replay recent history before the live hook path begins. Historical-only: reads the **completed** transcript files, not a live tail (bare flag = 5 minutes). |
| `--version`, `-V` | Print version and exit. |
| `--help`, `-h` | Print usage and exit. |

At startup laterm prints one plain-color line — `laterm <version> monitoring
<dir>/` — naming the project it renders for (tilde-collapsed when under your home
directory), so the window does not look dead. If that project's log directory
does not exist yet (Claude Code has not been started there), a second line warns
that there is no conversation log yet; laterm keeps running (it still receives
live turns via hooks, and only `--catch-up` needs the directory), and you can
paste text to render in the meantime.

## How It Works

1. **One-time setup.** `laterm --install-hooks` adds two hook entries —
   `UserPromptSubmit` and `Stop` — to Claude Code's `settings.json`, each
   invoking `laterm --hook <event>`.
2. **Per turn**, Claude Code runs `laterm --hook <event>`: a thin forwarder that
   reads the hook's stdin JSON and sends it over a Unix-domain socket to the
   running LaTerm window. The socket is chosen from the project directory named
   in the hook payload, so a globally-installed hook reaches only the matching
   window. If no window is listening, the forwarder exits silently — it never
   blocks Claude Code.
3. LaTerm extracts the turn text — your prompt from `UserPromptSubmit`, the
   assistant's reply from `Stop` — and scans it for LaTeX math.
4. Each expression is rendered to a PNG by the embedded RaTeX engine and emitted
   using the terminal's image protocol (kitty, imgcat, or Sixel — in that
   preference order). If rendering fails, the raw LaTeX is passed through as text.

Two limitations worth knowing: the `Stop` hook delivers only the turn's **final**
assistant text, so assistant prose written *before* a tool call within the same
turn is not shown live (it is still visible via `--catch-up`, which reads the
completed transcript). And the live hook path is unix-only — it does not work on
Windows.

Output is a **full echo**: the conversation text is mirrored verbatim, with each
math expression rendered as an image in place. Each entry is bracketed by a
matched open/close role marker pair — `(u)>` / `<(u)` for user entries, `[a]>` /
`<[a]` for assistant entries, `{p}>` / `<{p}` for pasted text — with the body
text tinted in the role's color, and followed by a blank-line separator. A small expression (single symbol,
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
- **Pre-tool-call prose not shown live.** The `Stop` hook delivers only the
  turn's final assistant text, so assistant prose written before a tool call in
  the same turn is not rendered live; `--catch-up` on the completed transcript
  does show it.
- **No Windows live path.** The hook transport is a Unix-domain socket; on
  Windows, live rendering and `--install-hooks`/`--hook` do not work. Paste
  still works. (See [ROADMAP.md](ROADMAP.md).)
- **Hooks must be installed.** Without `laterm --install-hooks`, Claude Code
  pushes nothing and only `--catch-up` and manual paste render anything.
- **Background detection is best-effort.** Glyph contrast relies on an OSC 11
  query; terminals that do not answer (within 200 ms) get a black-on-white
  fallback rather than theme-matched glyphs.

## Troubleshooting

Symptoms that look like bugs but usually aren't — a stale binary on `PATH`,
missing hook entries, terminal-specific sizing quirks — are collected under
**Project-specific traps** in [CONVENTIONS.md](CONVENTIONS.md), along with what to check
first for each.

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
make install  # copy the dist binary for this OS to ~/.local/bin (INSTALL_DIR to override)
```

Plain `cargo build --release` and `cargo test` work too — the Makefile is a thin
wrapper over cargo.

## License

MIT — see [LICENSE](LICENSE).

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
| [`windows-sys`](https://github.com/microsoft/windows-rs) v0.59 | Microsoft | MIT / Apache-2.0 | Windows-only: Console API for OSC 11 / DA1 / cell-size queries and terminal width; `FILE_SHARE_READ` for the log-file share mode |
