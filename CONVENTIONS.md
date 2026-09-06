# CONVENTIONS — LaTerm

House rules for working in this repository. Everything here is specific to
LaTerm — general engineering practice lives in your agent definition, and is not
repeated here.

---

## The document hierarchy

Four artifacts, four jobs. Keeping them separate is the point; blurring them is
how this project accumulates contradictions.

| Document | Answers | Authority |
|---|---|---|
| **SPEC.md** | What must be true for this to *be* LaTerm | Governs everything below it |
| **ARCHITECTURE.md** | How this Rust program achieves that | Governs the code |
| **CONVENTIONS.md** | How we work here | This file |
| **the code** | The running expression of ARCHITECTURE.md | Governs nothing — it is the thing being governed |

**Read SPEC.md and ARCHITECTURE.md before changing behavior.** Neither is
optional context, and neither is reconstructible from the code. The code tells
you what happens; it cannot tell you what was required, what was chosen, or what
was deliberately given up.

**When they disagree, the higher document wins and the lower one is the defect.**
Do not resolve a contradiction by editing the spec to match the code. If the spec
is genuinely wrong, say so and get it changed deliberately — that is a different
act from patching a mismatch.

### Which document does your change touch?

- Changing **what LaTerm does** — new behavior, a changed output format, a
  different CLI contract → **SPEC.md first**, then ARCHITECTURE.md, then the
  code. A behavior change that only lands in code is unrecorded.
- Changing **how it does it** — module boundaries, a swapped crate, a different
  transport → **ARCHITECTURE.md** and the code. SPEC.md must not need editing;
  if it does, the change is bigger than you think — stop and surface it.
- Adding a dependency → the justification table in **ARCHITECTURE.md §9**.
- Fixing a bug → no document changes. The docs already said what should have
  happened.

Requirements are numbered (`R-n.m`). Cite them; don't paraphrase them. A
paraphrase in a second location is a future contradiction.

---

## Ethos

**Full echo, not a filter.** LaTerm shows the whole conversation with math
rendered in place. It is tempting to make it smarter — summarize, skip
non-math lines, collapse repetition. Don't. Fidelity to the source is the
product.

**Fail loud at startup, never mid-stream.** Unsupported terminal: refuse to
start, clearly. A single expression that won't render: pass the raw LaTeX
through and keep going. The user's conversation must never be interrupted by
LaTerm's problems.

**Silence is a feature in the forwarder.** The `--hook` path exits 0 and says
nothing when no renderer is listening. This looks like a swallowed error and is
not — it is `R-2.4`. Do not add reporting, retries, or a fallback path to it.
Every byte it prints to stdout lands in the model's context.

---

## Project-specific traps

Things that have cost real debugging time here.

**"Catch-up works but nothing renders live" means the hooks are missing.** Check
for a `hooks` key in `.claude/settings.local.json`, `.claude/settings.json`, and
the global `settings.json` *before* suspecting the socket, the binary, or the
transport. The two paths share no machinery: catch-up only reads the transcript,
so it keeps working perfectly while the live path is dead — which makes it a
misleading "it's mostly working" signal.

Nothing will tell you. `R-2.4` requires the forwarder to exit 0 silently when no
renderer is listening, so a missing hook entry is indistinguishable from an idle
one: no log line, no warning, no stderr. That silence is correct — a hook that
blocks or fails Claude Code is far worse — but it means the diagnosis has to
start from the settings files.

Fix is `laterm --install-hooks`, then **restart the Claude Code session** — hooks
are read at session start, so the entries do not take effect in the session that
wrote them. Note also that `--install-hooks` freezes `current_exe()` as an
absolute path (`R-10.2`): install from the binary you actually intend to run, or
the hook will point at a path that no longer holds one.

**You are probably running a stale binary.** `~/.local/bin/laterm` (on `PATH`) and
`./target/debug/laterm` are different files, and `make build` refreshes only the
second. "The feature doesn't work" is usually this. Check which one you invoked
before investigating anything else.

**macOS kills an overwritten binary.** Copying over an existing binary in place
leaves a stale code-signing entry in the AMFI inode cache, and the next run dies
with `zsh: killed laterm`. `make install` removes before copying for exactly this
reason (ARCHITECTURE.md §7.3) — don't "simplify" it into a plain `cp`. A reboot
is not the fix; a fresh inode is.

**The directory-mangling rule is not ours.** It mirrors Claude Code's convention
byte for byte (SPEC.md §3.1), including `_` → `-`. It looks like a path-separator
transform and isn't. Changing it unilaterally breaks the rendezvous silently —
laterm keeps running and simply never receives a turn.

**You cannot verify rendering from an agent shell.** There is no tty, no
graphics protocol, and no terminal to answer OSC 11 or DA1. `make test` exercises
the parsers; everything downstream of `graphics::select` needs a human looking at
a real kitty/ghostty/iTerm2/Sixel window. Do not report image output as verified
when you have only run the test suite — say what you actually checked.

**iTerm2 misreports cell metrics after an in-session font resize**, which
mis-sizes images. If math suddenly renders at the wrong scale there, try a fresh
window before assuming a sizing bug.

---

## Build

All build, test, lint, and format operations go through the Makefile targets —
they are listed in ARCHITECTURE.md §7.1.
