# SPEC.md — LaTerm

What LaTerm **is**: the behavior any implementation must exhibit, independent of
language, module structure, or library choice. ARCHITECTURE.md describes how
*this* implementation achieves it and is subordinate to this document.

**MUST**/**MUST NOT** are requirements — violating one is a blocking defect.
**SHOULD** is a strong default, tradeable with a stated reason. **MAY** is
permission. Requirements are numbered `R-n.m` so other documents cite rather
than restate them.

---

## 1. Purpose

A terminal displays a coding agent's LaTeX as raw source. LaTerm renders that
math as images, live, in a second terminal window, running alongside the agent
rather than wrapping it. The same window also renders pasted text on demand.

---

## 2. Definitions

| Term | Meaning |
|---|---|
| **host agent** | The coding-agent process whose conversation is rendered (§3.1). |
| **renderer** | The long-running LaTerm process owning a viewing window, watching one project directory. |
| **forwarder** | A short-lived LaTerm process the host agent invokes once per turn to hand a turn to the renderer. |
| **project directory** | The absolute working directory shared by host agent and renderer. The identity by which a forwarder finds its renderer. |
| **turn** | One exchange step: a user prompt, or an assistant reply. |
| **entry** | One unit of rendered output, bracketed by exactly one marker pair — one live turn, one replayed transcript entry, or one paste. |
| **math span** | A region of text delimited as LaTeX math (`R-5.1`), classified *inline* or *display*. |
| **segment** | A contiguous run of an entry's text: either literal text or one math span. |
| **reference height** | Pixel height of a rendered capital `X`, measured once at startup. All sizing and layout is expressed as multiples of it. |

---

## 3. Given External Interfaces

**Not LaTerm's to define.** LaTerm conforms; it does not get a vote. Recorded
with the versions observed, because a change on the far side is a *specification*
change — new requirements — not an implementation defect.

### 3.1 Host agent — Claude Code (observed v2.1.220)

Claude Code invokes configured shell commands on named lifecycle events, passing
a JSON payload on the command's stdin. LaTerm consumes two:

| Event | Fields consumed | Carries |
|---|---|---|
| `UserPromptSubmit` | `hook_event_name`, `cwd`, `prompt` | The user's message text. |
| `Stop` | `hook_event_name`, `cwd`, `last_assistant_message` | The turn's **final** assistant text, untruncated. |

Three properties drive requirements elsewhere:

- `cwd` is the project directory as Claude Code reports it, and is
  authoritative — not necessarily the hook process's own working directory.
- **stdout of a `UserPromptSubmit` hook is injected into the model's context**
  (`R-11.2`).
- **`Stop` carries only the final assistant message** — prose emitted before a
  tool call in the same turn is never delivered (§5.1).

**Hook configuration** lives under a top-level `hooks` key in a `settings.json`:
`.claude/settings.json` (project, shared/committed),
`.claude/settings.local.json` (project, personal/untracked), or
`~/.claude/settings.json` (user, global). An entry has the shape:

```json
{ "hooks": { "<EventName>": [
    { "hooks": [ { "type": "command", "command": "<command line>" } ] } ] } }
```

**Transcript store.** Per-session `*.jsonl` transcripts under
`~/.claude/projects/<mangled-project-dir>/`, where the mangling replaces every
character not in `[A-Za-z0-9]` with `-`, one for one:

```
/Users/benn/projects/laterm          ->  -Users-benn-projects-laterm
/Users/benn/projects/agent_convos/x  ->  -Users-benn-projects-agent-convos-x
C:\Users\benn\projects\laterm        ->  C--Users-benn-projects-laterm
```

Note `_` → `-`: the rule is *not* "path separators only."

Relevant lines carry a top-level `type` of `"user"` or `"assistant"`, an RFC 3339
`timestamp`, and a `message.content` that is either a JSON array of typed blocks
(`type: "text"` blocks carry text) or a plain JSON string.

**The transcript is written asynchronously and lags in-memory state** — which is
why live turns arrive by push, not by tailing (`R-2.1`).

### 3.2 Terminal graphics protocols

| Protocol | Wire form | Detection |
|---|---|---|
| kitty graphics | `ESC _ G <control-data> ; <payload> ESC \` (APC); PNG as `f=100`, base64 in ≤4096-byte chunks, `a=T,f=100,r=<rows>` on the first chunk, `m=0` on the last | Env: `KITTY_WINDOW_ID` set, `TERM` containing `kitty`/`ghostty`, or `TERM_PROGRAM == "ghostty"` |
| iTerm2 imgcat | `ESC ] 1337 ; File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len> : <base64> BEL` | Env: `TERM_PROGRAM == "iTerm.app"`, `LC_TERMINAL == "iTerm2"`, or `TERM_PROGRAM == "WezTerm"` |
| Sixel | DCS raster stream: color registers on a 0–100 scale, 6-row bands, RLE, data chars based at `0x3F` | DA1 round-trip: reply contains attribute `4` |

### 3.3 Terminal query round-trips

| Query | Request | Reply parsed for |
|---|---|---|
| Background color | OSC 11 — `ESC ] 11 ; ?` | `rgb:RRRR/GGGG/BBBB` |
| Device attributes | DA1 — `ESC [ c` | `ESC [ ? <attrs> c`; attribute `4` = Sixel |
| Cell pixel size | `ESC [ 1 6 t` | Character-cell pixel height |

No terminal is required to answer. Every query is best-effort, and every consumer
has a defined fallback (`R-8.2`, `R-8.4`).

---

## 4. Requirements

### R-1 — Process model

- **R-1.1** LaTerm MUST run as a separate process in its own terminal window. It
  MUST NOT wrap, launch, proxy, or intercept the host agent, and MUST NOT spawn
  any child process.
- **R-1.2** **Non-interference.** LaTerm MUST NOT write to, modify, or otherwise
  disturb the host agent or its transcripts. Transcript access is read-only.
- **R-1.3** The sole exception to `R-1.2` is hook installation (`R-10`), an
  explicit user-invoked setup action, not part of monitoring operation.
- **R-1.4** A renderer is bound to exactly one project directory, fixed at
  startup (`R-9.2`), and renders that project's turns only.
- **R-1.5** LaTerm MUST ship as a self-contained binary named `laterm` with no
  external runtime dependency — math fonts MUST be embedded, not loaded from a
  font directory.

### R-2 — Live turn ingestion

- **R-2.1** Live turns MUST arrive by **push from the host agent**, not by
  tailing the transcript (which lags — §3.1).
- **R-2.2** The host agent invokes a **forwarder** per turn. It MUST read the
  payload from stdin, deliver those bytes unmodified to the renderer bound to the
  payload's `cwd`, and exit 0.
- **R-2.3** **Rendezvous.** Forwarder and renderer MUST derive their meeting
  point by the same deterministic function of the project directory, so a
  forwarder reaches the renderer watching that project and no other. The
  forwarder MUST derive it from the payload's `cwd` (§3.1), never from its own
  working directory.
- **R-2.4** **A forwarder MUST NEVER block or fail the host agent.** With no
  renderer listening it MUST exit 0 silently — rendering nothing, reporting
  nothing. This is the normal case when LaTerm is not running.
- **R-2.5** The renderer MUST accept turns one at a time in arrival order, so a
  prompt renders before the reply it provoked.
- **R-2.6** A payload whose `cwd` does not match the watched project directory
  MUST be dropped (a globally-installed hook fires for every project).
- **R-2.7** A payload with an unrecognized event name, empty content, or
  malformed JSON MUST be dropped without crashing the renderer.
- **R-2.8** `UserPromptSubmit` produces exactly one **user** entry from `prompt`;
  `Stop` exactly one **assistant** entry from `last_assistant_message`.
- **R-2.9** Each received turn renders exactly once. No live file reading, so no
  offset bookkeeping.

### R-3 — Historical catch-up

- **R-3.1** LaTerm MUST offer an opt-in replay of recent history from the
  **completed** transcript, run once at startup before live ingestion.
- **R-3.2** Catch-up scans every transcript file for the watched project, selects
  entries with `timestamp` at or after `now − N` minutes, sorts them across files
  chronologically, and renders in that order. `N` defaults to 5 when bare.
- **R-3.3** Entries with an absent or unparseable timestamp MUST be excluded.
- **R-3.4** Only `user` and `assistant` entries produce output. Array
  `message.content` contributes its `text` blocks; string content contributes
  itself. All else — other types, empty content, unparseable lines — yields
  nothing, without crashing.
- **R-3.5** One source entry produces exactly one output entry, framed
  byte-identically to the live path (`R-6`).
- **R-3.6** Catch-up is historical-only: read-only file access, and it MUST NOT
  interact with the live transport.

### R-4 — Manual input

- **R-4.1** Pasted text MUST be captured and rendered as **one** entry through
  the same path as a conversation turn, however many lines it spans.
- **R-4.2** Typed input MUST NOT be rendered or echoed.
- **R-4.3** A printable keystroke MUST produce an audible bell, rate-limited to
  roughly one per 250 ms. Escape sequences and other control bytes are consumed
  silently.
- **R-4.4** Enter/Return MUST write a manual topic separator: blank line,
  terminal-width rule of `═` (U+2550) in bold yellow, newline. Width falls back
  to 80 columns when undeterminable.
- **R-4.5** Separators MUST NOT stack: if the last output was already a
  separator, Enter bells instead. Any rendered content re-arms it.
- **R-4.6** The interrupt keystroke MUST still signal the process (`R-13.1`) —
  raw input mode MUST NOT swallow it.

### R-5 — Math scanning

- **R-5.1** Recognized delimiters: `$$…$$` and `\[…\]` (display); `$…$` and
  `\(…\)` (inline).
- **R-5.2** `\$` is a literal dollar and MUST NOT open a span.
- **R-5.3** A display span MAY cross lines.
- **R-5.4** Unterminated spans and empty spans (e.g. `$$`) MUST be treated as
  literal text.
- **R-5.5** Multiple spans on one line MUST each be recognized in document order,
  with intervening text preserved between them. No span duplicated or dropped.
- **R-5.6** Scanning MUST NOT drop content: every part of the input is
  represented in exactly one segment, including newlines and math-free lines —
  no windowing, anchoring, or discarding. Text segments hold source verbatim.
  Math segments hold the **inner expression only** — delimiters stripped,
  whitespace trimmed — so the stream is not a byte-for-byte round trip of an
  input containing math.

### R-6 — Output format

- **R-6.1** **Full echo.** All conversation text MUST be echoed verbatim,
  including messages and lines with no math.
- **R-6.2** Each entry MUST be bracketed by a matched pair of bold, color-coded
  role markers — **one pair per entry, not per line**:

  | Role | Open | Close | Color |
  |---|---|---|---|
  | user | `(u)> ` | `<(u)` | bold green |
  | assistant | `[a]> ` | `<[a]` | bold cyan |
  | paste | `{p}> ` | `<{p}` | bold magenta |

- **R-6.3** Closing-marker placement follows the cursor: appended to the last
  line after a space when content ends mid-line; on its own line when content
  ends at column 0 — after a block image, or after text ending in a newline.
- **R-6.4** An entry's prose MUST be tinted in the role's non-bold color, so the
  entry stays identifiable at any scroll position. The bold markers are the
  primary role signal and the colorblind backstop; the tint is secondary.
- **R-6.5** Math images MUST render neutral — untinted; their color comes from
  contrast (`R-8.2`).
- **R-6.6** Color MUST be reset by the closing marker, before the separator, so
  separators are never tinted.
- **R-6.7** Role colors MUST come from the basic 8-color ANSI palette, tracking
  the user's terminal theme rather than imposing fixed hues.
- **R-6.8** Each entry is followed by exactly one blank-line separator.
- **R-6.9** An entry with no renderable content emits nothing — no marker, no
  separator.

### R-7 — Math rendering and sizing

- **R-7.1** Every recognized span MUST be rendered to an image and emitted in
  place, in document order, via the terminal's image protocol.
- **R-7.2** **Image height MUST scale with the terminal's text size**, so math
  reads at the scale of the prose beside it:

  ```
  rows = max(1, round(image_height_px / reference_height_px × 1.25))
  ```

  The `1.25` is a legibility bump, sizing math to the prose's full line height
  rather than its cap height.
- **R-7.3** **Layout follows rendered height.** A span ≥ 1.5 × the reference
  height is a **block** — newline, image, newline, on its own line. Shorter spans
  stay **inline**. Both emit at the same `rows` from `R-7.2`.
- **R-7.4** The reference height MUST be measured at runtime, once, by rendering
  a capital `X`, so sizing tracks actual renderer output rather than hard-coded
  pixels. An implementation-defined fallback applies if that render fails.
- **R-7.5** **A rendering failure MUST NOT be fatal and MUST NOT lose content.**
  The expression MUST be passed through as text, re-delimited so it stays
  recognizable as math (`$…$` inline, `$$…$$` display — the original delimiter
  spelling is gone, since `R-5.6` did not retain it), and processing MUST
  continue with the next segment.
- **R-7.6** A rendered image exceeding 4096 × 4096 px MUST be rejected and
  handled as a rendering failure per `R-7.5`.
- **R-7.7** There MUST NOT be a command allowlist. Coverage is bounded by the
  math renderer's own capability; anything it rejects degrades via `R-7.5`.

### R-8 — Graphics protocol and contrast

- **R-8.1** **Graphics-only, fail loud.** LaTerm MUST select an image protocol at
  startup, preferring **kitty → imgcat → Sixel**. Supporting none means a clear
  error to stderr and immediate non-zero exit, doing no further work. **There is
  no Unicode text fallback.**
- **R-8.2** **Contrast.** At startup LaTerm queries the terminal background
  (§3.3) and renders glyphs in a contrasting color — light on dark, dark on
  light — over a transparent background.
- **R-8.3** Where a protocol does not reliably honor transparency, an opaque
  background filled with the detected terminal color MAY be substituted.
- **R-8.4** If the background query goes unanswered, LaTerm MUST fall back to
  black glyphs on opaque white, legible on any theme. Detection failure MUST NOT
  be fatal.
- **R-8.5** Protocol selection and background detection happen **once** per run.
  A probe requiring a terminal round-trip MUST NOT be repeated per image.

### R-9 — Command-line surface

- **R-9.1** Accepted invocations:

  ```
  laterm [-C <PATH>] [--log <PATH>] [--catch-up[=<MINS>]] [--version] [--help]
  laterm --install-hooks [--project|--project-local|--global]
  laterm --hook <UserPromptSubmit|Stop>
  ```

  | Flag | Effect |
  |---|---|
  | `-C`, `--cwd <PATH>` | Watch `PATH` instead of the process working directory. |
  | `--log <PATH>` | Enable logging to `PATH` (`R-12`). |
  | `--catch-up[=<MINS>]` | Replay recent history first (`R-3`); bare = 5 minutes. |
  | `--install-hooks` | Configure the host agent, then exit (`R-10`). |
  | `--hook <event>` | Forwarder mode (`R-2.2`); invoked by the host agent, not a user. |
  | `--version`, `-V` | Print version and exit. |
  | `--help`, `-h` | Print usage and exit. |

  A flag taking an argument MUST accept both the separated (`--log PATH`) and
  `=`-joined (`--log=PATH`) spellings.

- **R-9.2** `-C`/`--cwd` MUST change the directory used to derive **both** the
  watched transcript location and the rendezvous point, taking effect before
  either is derived, so the two cannot disagree. Failure to change directory is
  an error to stderr and a non-zero exit.
- **R-9.3** An unknown flag, or a missing/empty argument to a flag requiring one,
  MUST print usage to stderr and exit non-zero. `--hook` is the sole exception
  (`R-2.4`): a missing renderer is a silent exit 0.
- **R-9.4** `--install-hooks` accepts at most one scope flag; more than one is a
  usage error, as is a scope flag without `--install-hooks`.
- **R-9.5** If the working directory cannot be determined, LaTerm MUST error to
  stderr and exit non-zero.
- **R-9.6** **Startup line.** After protocol selection and directory derivation,
  the renderer MUST print one plain-color line — no role marker, no role color —
  so the window never looks dead:

  ```
  laterm <version> monitoring <dir>/
  ```

  `<dir>` is the derived location, tilde-collapsed when under home, trailing `/`.
- **R-9.7** **A missing transcript directory is not an error.** The renderer
  prints a second plain-color line saying there is no conversation log *yet* and
  that pasting still works, then **keeps running** — live turns arrive by push
  regardless, and only catch-up needs the directory. This is the normal state
  when LaTerm starts before the host agent.

### R-10 — Hook installation

- **R-10.1** `--install-hooks` MUST write the two hook entries (§3.1) for
  `UserPromptSubmit` and `Stop` into the target settings file, then exit.
- **R-10.2** Each entry MUST invoke LaTerm by **absolute path** — the running
  executable's — so the hook fires regardless of the host agent's `PATH`.
- **R-10.3** Scope: `--project` → shared/committed; `--project-local` →
  personal/untracked; `--global` → user. **Default `--project-local`**: never
  touch shared, committed settings unless explicitly directed.
- **R-10.4** The merge MUST be **additive and idempotent**. Unrelated top-level
  keys (e.g. `permissions`) and unrelated hook entries MUST survive; re-running
  MUST NOT duplicate LaTerm's entries.
- **R-10.5** **Hook installation is required for the live path.** Without it the
  host agent pushes nothing and only catch-up and paste render. A documented
  precondition, not a failure mode to detect.

### R-11 — stdout discipline

- **R-11.1** The renderer's stdout carries **only**: the startup line and
  optional missing-directory warning; role markers; role-tinted body text;
  verbatim conversation text; image-protocol escapes; blank-line entry
  separators; the manual separator rule; the bell. No diagnostics, progress, or
  status.
- **R-11.2** **The forwarder MUST write nothing to stdout, ever.** A
  `UserPromptSubmit` hook's stdout is injected into the model's context (§3.1);
  anything printed there contaminates the conversation LaTerm exists to observe.
- **R-11.3** Diagnostics go to a log file or stderr. Only fatal pre-exit messages
  go to stderr.

### R-12 — Logging

- **R-12.1** Logging is **opt-in, off by default**, with no default path — only
  when `--log <PATH>` is given.
- **R-12.2** Logging MUST NEVER write to stdout (`R-11.1`).
- **R-12.3** The log file is opened for append with owner-only permissions where
  the platform expresses them.
- **R-12.4** Single writer: an instance that cannot acquire the exclusive claim
  MUST warn once to stderr and **keep running without logging**. Concurrent
  readers (`tail -f`) MUST be unaffected.
- **R-12.5** If the file cannot be opened at all, warn once to stderr and run
  without logging. A logging problem never stops rendering.
- **R-12.6** Verbosity from `LATERM_LOG_LEVEL` (`debug`, `info`, `warn`, `error`;
  default `info`) when logging is enabled.
- **R-12.7** Raw conversation content is logged only at `debug`.

### R-13 — Lifecycle

- **R-13.1** Interrupt and terminate signals MUST stop ingestion cleanly, release
  the rendezvous point, and exit 0.
- **R-13.2** The renderer MUST reclaim a rendezvous point left by a crashed
  predecessor rather than refusing to start.
- **R-13.3** Terminal modes the renderer changes (raw input, bracketed paste)
  MUST be restored on exit, including a panicking exit.

---

## 5. Accepted Limitations

Consequences of §3 or of deliberate trades — specified behavior, not defects. No
implementation is expected to overcome them.

- **5.1 — Pre-tool-call assistant prose is not rendered live.** `Stop` delivers
  only the turn's final assistant text (§3.1), so prose written before a tool
  call in the same turn is never delivered. Catch-up on the completed transcript
  does show it. Deliberate trade: the telemetry-based alternative truncates
  content at a fixed size, which long, math-dense answers exceed.
- **5.2 — A turn pushed with no renderer running is lost from the live feed.**
  Per `R-2.4` the forwarder exits silently; such turns remain visible to
  catch-up.
- **5.3 — Background detection is best-effort.** Non-answering terminals get the
  black-on-white fallback (`R-8.4`) rather than theme-matched glyphs.
- **5.4 — Graphics-protocol terminals only.** Per `R-8.1`, a terminal supporting
  none of the three is rejected at startup. There is no degraded text mode.

---

## 6. Out of Scope

LaTerm is not, and does not aim to be: a wrapper, launcher, supervisor, or proxy
for the host agent (`R-1.1`); an input surface — it never sends anything to the
host agent and does not accept typed text (`R-4.2`); a general Markdown renderer
— only math is transformed, everything else is echoed verbatim (`R-6.1`); a
scrollback manager, pager, or TUI — output is an append-only stream the terminal
itself scrolls; or a transcript archiver, editor, or search tool.

---

## 7. Verification

Every requirement above is stated in observable terms and is its own acceptance
criterion. There is no separate acceptance-criteria list to keep in sync.
