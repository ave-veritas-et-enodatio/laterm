# SPEC.md — LaTerm

What LaTerm **is**. This document defines the behavior any implementation must
exhibit to be LaTerm, independent of language, module structure, or library
choices.

- **This document** — what must be true. Normative.
- **ARCHITECTURE.md** — how *this* implementation achieves it. Mechanism,
  modules, crates, build. Subordinate to this document.
- **AGENTS.md** — house rules for working in this repository.
- **the code** — the running expression of ARCHITECTURE.md.

Where this document and ARCHITECTURE.md disagree, this document governs and
ARCHITECTURE.md is the defect. Where ARCHITECTURE.md and the code disagree, one
of them is the defect and it must be resolved, not left standing.

**MUST** / **MUST NOT** are requirements: violating one is a blocking defect.
**SHOULD** is a strong default that may be traded away with a stated reason.
**MAY** is permission. Requirements are numbered (`R-n.m`) so ARCHITECTURE.md
can cite them rather than restate them.

---

## 1. Purpose

A developer is working with a coding agent in a terminal. The agent's replies
contain LaTeX math, which the terminal renders as raw source — `$\hat{H}\psi =
E\psi$` rather than the expression.

LaTerm renders that math as images, live, in a second terminal window. It runs
alongside the agent rather than wrapping it: the developer keeps their normal
agent session untouched and reads the math in a companion window.

Secondary purpose: the same window accepts pasted text, so math from any source
can be rendered on demand.

---

## 2. Definitions

| Term | Meaning |
|---|---|
| **host agent** | The coding-agent process whose conversation is rendered. See §3.1 for the concrete binding. |
| **renderer** | The long-running LaTerm process that owns a viewing window and watches one project directory. |
| **forwarder** | A short-lived LaTerm process the host agent invokes once per turn to hand a turn to the renderer. |
| **project directory** | The absolute working directory shared by the host agent and the renderer. It is the identity by which a forwarder finds its renderer. |
| **turn** | One exchange step in the conversation: a user prompt, or an assistant reply. |
| **entry** | One unit of rendered output, bracketed by exactly one marker pair. One live turn, one replayed transcript entry, or one paste. |
| **math span** | A region of text delimited as LaTeX math (§R-5.1), classified *inline* or *display*. |
| **segment** | A contiguous run of an entry's text that is either literal text or one math span. An entry's segments concatenate back to its exact input. |
| **reference height** | The pixel height of a rendered capital `X`, measured once at startup. All image sizing and layout decisions are expressed as multiples of it. |

---

## 3. Given External Interfaces

These contracts are **not LaTerm's to define**. LaTerm conforms to them; it does
not get a vote. They are recorded here with the versions against which they were
observed, because a change on the far side is a *specification* change — new
requirements — not an implementation defect.

### 3.1 Host agent — Claude Code

Observed against Claude Code **v2.1.220**.

**Hook events.** Claude Code invokes configured shell commands on named
lifecycle events, passing a JSON payload on the command's stdin. LaTerm consumes
two:

| Event | Payload fields consumed | Carries |
|---|---|---|
| `UserPromptSubmit` | `hook_event_name`, `cwd`, `prompt` | The user's message text. |
| `Stop` | `hook_event_name`, `cwd`, `last_assistant_message` | The turn's **final** assistant text, untruncated. |

`cwd` is the project directory as Claude Code reports it, and is authoritative —
it is not necessarily the working directory of the hook process.

**stdout of a `UserPromptSubmit` hook is injected into the model's context.**
This is the reason for R-11.2.

**`Stop` carries only the final assistant message.** Assistant prose emitted
*before* a tool call in the same turn is not delivered. See §5.1.

**Hook configuration** lives under a top-level `hooks` key in a `settings.json`,
with three scopes:

| Scope | File |
|---|---|
| project, shared/committed | `.claude/settings.json` |
| project, personal/untracked | `.claude/settings.local.json` |
| user, global | `~/.claude/settings.json` |

An entry has the shape:

```json
{
  "hooks": {
    "<EventName>": [
      { "hooks": [ { "type": "command", "command": "<command line>" } ] }
    ]
  }
}
```

**Transcript store.** Claude Code writes per-session `*.jsonl` transcripts to
`~/.claude/projects/<mangled-project-dir>/`, where the mangling replaces every
character not in `[A-Za-z0-9]` with `-`, one for one:

```
/Users/benn/projects/laterm            ->  -Users-benn-projects-laterm
/Users/benn/projects/agent_convos/x    ->  -Users-benn-projects-agent-convos-x
C:\Users\benn\projects\laterm          ->  C--Users-benn-projects-laterm
```

Note the `_` → `-` mapping: the rule is *not* "path separators only."

Transcript lines relevant to LaTeX are objects with a top-level `type` of
`"user"` or `"assistant"`, an RFC 3339 `timestamp`, and a `message.content` that
is either a JSON array of typed blocks (of which `type: "text"` blocks carry
text) or a plain JSON string.

**The transcript is written asynchronously and lags in-memory state.** This is
the reason live turns arrive by hook push rather than by tailing the file
(R-2.1).

### 3.2 Terminal graphics protocols

| Protocol | Wire form | Detection |
|---|---|---|
| kitty graphics | `ESC _ G <control-data> ; <payload> ESC \` (APC), PNG as `f=100`, base64 payload in ≤4096-byte chunks, `a=T,f=100,r=<rows>` on the first chunk, `m=0` on the last | Environment: `KITTY_WINDOW_ID` set, `TERM` containing `kitty` or `ghostty`, or `TERM_PROGRAM == "ghostty"` |
| iTerm2 imgcat | `ESC ] 1337 ; File=inline=1;height=<rows>;preserveAspectRatio=1;size=<len> : <base64> BEL` | Environment: `TERM_PROGRAM == "iTerm.app"`, `LC_TERMINAL == "iTerm2"`, or `TERM_PROGRAM == "WezTerm"` |
| Sixel | DCS-introduced raster stream: color registers on a 0–100 scale, 6-row bands, run-length encoded, data characters based at `0x3F` | Terminal round-trip: DA1 (`ESC [ c`) reply `ESC [ ? <attrs> c` contains attribute `4` |

### 3.3 Terminal query round-trips

| Query | Request | Reply parsed for |
|---|---|---|
| Background color | OSC 11 — `ESC ] 11 ; ?` | `rgb:RRRR/GGGG/BBBB` |
| Device attributes | DA1 — `ESC [ c` | `ESC [ ? <attrs> c`, attribute `4` = Sixel |
| Cell pixel size | `ESC [ 1 6 t` | Character-cell pixel height |

Terminals are not required to answer any of these. Every query is best-effort
and every consumer of one has a defined fallback (R-8.2, R-8.4).

---

## 4. Requirements

### R-1 — Process model

- **R-1.1** LaTerm MUST run as a process separate from the host agent, in its
  own terminal window. It MUST NOT wrap, launch, proxy, or intercept the host
  agent, and MUST NOT spawn any child process.
- **R-1.2** **Non-interference.** LaTerm MUST NOT write to, modify, truncate, or
  otherwise disturb the host agent's process or its transcript files. Transcript
  access is read-only, always.
- **R-1.3** The single exception to R-1.2 is the explicit, user-invoked hook
  installation command (R-10), which writes one configuration file. It is a
  setup action, not part of monitoring operation.
- **R-1.4** A renderer is bound to exactly one project directory, determined at
  startup (R-9.2). It renders that project's turns and no others.
- **R-1.5** LaTerm MUST ship as a self-contained binary named `laterm`, with no
  external runtime dependency — in particular, math fonts MUST be embedded, not
  loaded from a font directory at runtime.

### R-2 — Live turn ingestion

- **R-2.1** Live turns MUST be received by **push from the host agent**, not by
  tailing the transcript. (The transcript lags live state — §3.1.)
- **R-2.2** The host agent invokes a **forwarder** per turn. The forwarder MUST
  read the hook payload from its stdin, deliver those bytes unmodified to the
  renderer bound to the payload's `cwd`, and exit 0.
- **R-2.3** **Rendezvous.** The forwarder and the renderer MUST derive their
  meeting point by the same deterministic function of the project directory, so
  that a forwarder always reaches the renderer watching that project — and no
  other. The forwarder MUST derive it from the payload's `cwd` (§3.1), never
  from its own working directory.
- **R-2.4** **A forwarder MUST NEVER block or fail the host agent.** If no
  renderer is listening, the forwarder MUST exit 0 silently, rendering nothing
  and reporting nothing. This is the normal case when LaTerm is not running.
- **R-2.5** The renderer MUST accept turns one at a time in arrival order, so a
  prompt is rendered before the reply it provoked.
- **R-2.6** A received payload whose `cwd` does not match the renderer's watched
  project directory MUST be dropped without rendering. (A globally-installed
  hook fires for every project.)
- **R-2.7** A payload with an unrecognized event name, empty content, or
  malformed JSON MUST be dropped without rendering. Malformed input MUST NOT
  crash the renderer.
- **R-2.8** `UserPromptSubmit` produces exactly one **user** entry from
  `prompt`. `Stop` produces exactly one **assistant** entry from
  `last_assistant_message`, untruncated.
- **R-2.9** Each received turn is rendered exactly once. There is no live file
  reading and therefore no offset bookkeeping.

### R-3 — Historical catch-up

- **R-3.1** LaTerm MUST offer an opt-in replay of recent history from the
  **completed** transcript, run once at startup before live ingestion begins.
- **R-3.2** Catch-up scans every transcript file for the watched project,
  selects entries whose `timestamp` is at or after `now − N` minutes, sorts them
  across files into chronological order, and renders them in that order.
  `N` defaults to 5 when the option is given bare.
- **R-3.3** Entries with an absent or unparseable timestamp MUST be excluded.
- **R-3.4** Only `user` and `assistant` entries produce output. Array
  `message.content` contributes its `text` blocks; string `message.content`
  contributes itself. Every other entry type, empty content, and unparseable
  lines yield nothing, without crashing.
- **R-3.5** One source transcript entry produces exactly one output entry — its
  framing MUST be byte-identical to the live path's (R-6).
- **R-3.6** Catch-up is historical-only and strictly separate from live
  ingestion. It reads files read-only and MUST NOT interact with the live
  transport.

### R-4 — Manual input

- **R-4.1** Text pasted into the renderer's window MUST be captured and rendered
  as one entry, through the same path as a conversation turn. A multi-line paste
  is one entry, not one per line.
- **R-4.2** Typed input MUST NOT be rendered and MUST NOT be echoed. LaTerm is a
  viewer, not a prompt.
- **R-4.3** A printable keystroke MUST produce an audible bell, rate-limited to
  roughly one per 250 ms. Escape sequences and other control bytes are consumed
  silently.
- **R-4.4** Enter/Return MUST write a manual topic separator: a blank line, a
  terminal-width rule of `═` (U+2550) in bold yellow, and a newline. Terminal
  width falls back to 80 columns when it cannot be determined.
- **R-4.5** Separators MUST NOT stack: if the last output was already a manual
  separator, Enter produces the bell instead. Any rendered content re-arms it.
- **R-4.6** The interrupt keystroke MUST continue to signal the process (R-13.1)
  — raw input mode MUST NOT swallow it.

### R-5 — Math scanning

- **R-5.1** Recognized delimiters:

  | Delimiters | Class |
  |---|---|
  | `$$…$$`, `\[…\]` | display |
  | `$…$`, `\(…\)` | inline |

- **R-5.2** `\$` is a literal dollar sign and MUST NOT open a math span.
- **R-5.3** A display span MAY span multiple lines.
- **R-5.4** An unterminated span, and an empty span (e.g. `$$`), MUST be treated
  as literal text.
- **R-5.5** Multiple spans on one line MUST each be recognized, in document
  order, with the text between them preserved between them. No span may be
  duplicated or dropped.
- **R-5.6** Scanning MUST NOT drop content. Every part of the input is
  represented in exactly one segment, including newlines and text on lines
  containing no math — there is no windowing, anchoring, or discarding. Text
  segments hold their source verbatim. Math segments hold the **inner
  expression only**: the delimiters are stripped and surrounding whitespace is
  trimmed, so the segment stream is not a byte-for-byte round trip of an input
  containing math.

### R-6 — Output format

- **R-6.1** **Full echo.** All conversation text MUST be echoed verbatim,
  including messages and lines containing no math. LaTerm is not a filter.
- **R-6.2** Each entry MUST be bracketed by a matched pair of bold, color-coded
  role markers — **one pair per entry, not per line**:

  | Role | Open | Close | Color |
  |---|---|---|---|
  | user | `(u)> ` | `<(u)` | bold green |
  | assistant | `[a]> ` | `<[a]` | bold cyan |
  | paste | `{p}> ` | `<{p}` | bold magenta |

- **R-6.3** Placement of the closing marker follows the cursor: when the entry's
  content ends mid-line it is appended to that line, separated by a space; when
  the content ends at column 0 — after a block image, or after text ending in a
  newline — it falls to its own line.
- **R-6.4** An entry's prose MUST be tinted in the role's non-bold color, so the
  entry stays identifiable at any scroll position. Bold markers are the primary
  role signal and the colorblind backstop; the tint is a secondary orientation
  cue.
- **R-6.5** Math images MUST render neutral — untinted (their color is set by
  contrast, R-8.2).
- **R-6.6** Color MUST be reset by the closing marker, before the separator, so
  separators are never tinted.
- **R-6.7** Role colors MUST come from the basic 8-color ANSI palette, so they
  track the user's terminal theme rather than imposing fixed hues.
- **R-6.8** Each entry is followed by exactly one blank-line separator.
- **R-6.9** An entry with no renderable content emits nothing at all — no
  marker, no separator.

### R-7 — Math rendering and sizing

- **R-7.1** Every recognized math span MUST be rendered to an image and emitted
  in place, in document order, using the terminal's image protocol.
- **R-7.2** **Image height MUST scale with the terminal's text size**, so math
  reads at the same visual scale as the prose beside it:

  ```
  rows = max(1, round(image_height_px / reference_height_px × 1.25))
  ```

  The `1.25` factor is a legibility bump, sizing math to the prose's full line
  height rather than its cap height.
- **R-7.3** **Layout follows rendered height.** A span whose rendered height is
  ≥ 1.5 × the reference height is a **block**: newline, image, newline, on its
  own line. A shorter span stays **inline**, in the text flow. Both layouts emit
  at the same `rows` from R-7.2.
- **R-7.4** The reference height MUST be measured at runtime, once, by rendering
  a capital `X` — so sizing tracks the renderer's actual output rather than
  hard-coded pixel counts. If that measurement fails, an implementation-defined
  fallback is used.
- **R-7.5** **A rendering failure MUST NOT be fatal, and MUST NOT lose content.**
  On any parse or render error the expression MUST be passed through as text,
  re-delimited so it remains recognizable as math (`$…$` inline, `$$…$$`
  display — the original delimiter spelling is not preserved, since `R-5.6`
  did not retain it), and processing MUST continue with the next segment. The
  process MUST NOT exit because a span failed to render.
- **R-7.6** A rendered image exceeding 4096 × 4096 px MUST be rejected and
  handled as a rendering failure per R-7.5.
- **R-7.7** There MUST NOT be a command allowlist. Coverage is bounded by the
  math renderer's own capability, and anything it rejects degrades via R-7.5.

### R-8 — Graphics protocol and contrast

- **R-8.1** **Graphics-only, fail loud.** LaTerm MUST select an image protocol
  at startup, preferring **kitty → imgcat → Sixel**. If the terminal supports
  none, LaTerm MUST print a clear error to stderr and exit non-zero
  immediately, doing no further work. **There is no Unicode text fallback.**
- **R-8.2** **Contrast.** At startup LaTerm queries the terminal background
  (§3.3) and renders glyphs in a contrasting color — light on dark, dark on
  light — over a transparent background.
- **R-8.3** Where a protocol does not reliably honor transparency, an opaque
  background filled with the detected terminal color MAY be substituted.
- **R-8.4** If the background query is unanswered, LaTerm MUST fall back to
  black glyphs on an opaque white background, which is legible on any theme.
  Detection failure MUST NOT be fatal.
- **R-8.5** Protocol selection and background detection each happen **once** per
  run. A protocol probe that requires a terminal round-trip MUST NOT be repeated
  per image.

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
  | `--log <PATH>` | Enable logging to `PATH` (R-12). |
  | `--catch-up[=<MINS>]` | Replay recent history first (R-3); bare = 5 minutes. |
  | `--install-hooks` | Configure the host agent, then exit (R-10). |
  | `--hook <event>` | Forwarder mode (R-2.2); invoked by the host agent, not by a user. |
  | `--version`, `-V` | Print version and exit. |
  | `--help`, `-h` | Print usage and exit. |

  A flag taking an argument MUST accept both the separated (`--log PATH`) and
  the `=`-joined (`--log=PATH`) spelling.

- **R-9.2** `-C`/`--cwd` MUST change the directory used to derive **both** the
  watched transcript location and the rendezvous point, and MUST take effect
  before either is derived — so the two cannot disagree. A failure to change
  directory is an error to stderr and a non-zero exit.
- **R-9.3** An unknown flag, or a missing or empty argument to a flag that
  requires one, MUST print usage to stderr and exit non-zero. `--hook` mode is
  the sole exception (R-2.4): a missing renderer is a silent exit 0.
- **R-9.4** `--install-hooks` accepts at most one scope flag; more than one is a
  usage error, as is a scope flag given without `--install-hooks`.
- **R-9.5** If the process working directory cannot be determined, LaTerm MUST
  print an error to stderr and exit non-zero.
- **R-9.6** **Startup line.** After selecting a protocol and deriving the
  watched location, the renderer MUST print one plain-color line naming the
  version and the project it renders for — so the window never looks dead:

  ```
  laterm <version> monitoring <dir>/
  ```

  `<dir>` is the derived location, tilde-collapsed when under the user's home
  directory, with a trailing `/`. This line carries no role marker and no role
  color.
- **R-9.7** **A missing transcript directory is not an error.** If it does not
  exist, the renderer prints a second plain-color line stating that there is no
  conversation log *yet* and that pasting still works, and **keeps running** —
  live turns arrive by push regardless, and only catch-up needs the directory.
  This is the normal state when LaTerm is started before the host agent.

### R-10 — Hook installation

- **R-10.1** `--install-hooks` MUST write the two hook entries (§3.1) for
  `UserPromptSubmit` and `Stop` into the target settings file, then exit.
- **R-10.2** Each entry MUST invoke LaTerm by **absolute path** — the path of
  the running executable — so the hook fires regardless of the host agent's
  `PATH`.
- **R-10.3** Scope selection: `--project` → shared/committed settings;
  `--project-local` → personal/untracked settings; `--global` → user settings.
  **The default is `--project-local`**: never touch shared, committed settings
  unless explicitly directed.
- **R-10.4** The merge MUST be **additive and idempotent**. Unrelated top-level
  keys (e.g. a `permissions` block) and unrelated existing hook entries MUST
  survive; re-running MUST NOT duplicate LaTerm's entries.
- **R-10.5** **Hook installation is required for the live path.** Without it the
  host agent pushes nothing, and only catch-up and paste render anything. This
  is a documented precondition, not a failure mode to detect.

### R-11 — stdout discipline

- **R-11.1** The renderer's stdout carries **only**: the startup line and the
  optional missing-directory warning; role markers; role-tinted body text;
  verbatim conversation text; image-protocol escape sequences; blank-line entry
  separators; the manual separator rule; and the bell. Nothing else — no
  diagnostics, no progress, no status.
- **R-11.2** **The forwarder MUST write nothing to stdout under any
  circumstance.** A `UserPromptSubmit` hook's stdout is injected into the
  model's context (§3.1); anything printed there contaminates the conversation
  LaTerm exists to observe.
- **R-11.3** Diagnostics go to a log file or stderr. Only fatal pre-exit
  messages go to stderr.

### R-12 — Logging

- **R-12.1** Logging is **opt-in and off by default**. There is no default log
  path; logging happens only when `--log <PATH>` is given.
- **R-12.2** Logging MUST NEVER write to stdout (R-11.1).
- **R-12.3** The log file is opened for append, with owner-only permissions
  where the platform expresses them.
- **R-12.4** A single writer at a time: an instance that cannot acquire the
  exclusive write claim MUST print one warning to stderr and **continue running
  without logging**. Concurrent readers (`tail -f`) MUST be unaffected.
- **R-12.5** If the log file cannot be opened at all, LaTerm prints one warning
  to stderr and runs without logging. A logging problem never stops rendering.
- **R-12.6** Verbosity is set by the `LATERM_LOG_LEVEL` environment variable
  (`debug`, `info`, `warn`, `error`; default `info`) when logging is enabled.
- **R-12.7** Raw conversation content is logged only at `debug`.

### R-13 — Lifecycle

- **R-13.1** Interrupt and terminate signals MUST stop ingestion cleanly,
  release the rendezvous point, and exit 0.
- **R-13.2** The renderer MUST reclaim a rendezvous point left behind by a
  previous crashed instance, rather than refusing to start.
- **R-13.3** Terminal modes the renderer changes (raw input, bracketed paste)
  MUST be restored on exit, including on a panicking exit.

---

## 5. Accepted Limitations

These follow from the given interfaces of §3 or from deliberate trades. They are
specified behavior, not defects, and an implementation is not expected to
overcome them.

- **5.1 — Pre-tool-call assistant prose is not rendered live.** The `Stop` hook
  delivers only the turn's final assistant text (§3.1). Prose written before a
  tool call in the same turn is never delivered and is therefore not echoed
  live. Catch-up on the completed transcript does show it. This is a deliberate
  trade against the alternative telemetry-based sources, which truncate content
  at a fixed size — a dealbreaker for long, math-dense answers.

- **5.2 — A turn pushed with no renderer running is lost from the live feed.**
  By R-2.4 the forwarder exits silently. Turns produced while LaTerm is not
  running are simply not shown live; they remain visible to catch-up afterward.

- **5.3 — Background detection is best-effort.** Terminals that do not answer
  the background query get the black-on-white fallback (R-8.4) rather than
  theme-matched glyphs.

- **5.4 — Graphics-protocol terminals only.** By R-8.1 a terminal supporting
  none of the three protocols is rejected at startup. There is no degraded text
  mode.

---

## 6. Out of Scope

LaTerm is not, and does not aim to be:

- a wrapper, launcher, supervisor, or proxy for the host agent (R-1.1);
- an input surface — it never sends anything to the host agent, and typed text
  is not accepted (R-4.2);
- a general Markdown renderer — only math is transformed; everything else is
  echoed verbatim (R-6.1);
- a scrollback manager, pager, or TUI — output is an append-only stream that the
  terminal itself scrolls;
- a transcript archiver, editor, or search tool.

---

## 7. Verification

Every requirement above is stated in observable terms and is the acceptance
criterion for itself. There is no separate acceptance-criteria list to keep in
sync.
