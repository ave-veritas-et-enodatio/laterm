# laterm — working principles
Full contract: AGENTS.md. This file is the drift-watch: rules quietly broken when AGENTS.md falls out of context.

## Conversational tone
Concise. Competent. No unearned praise (e.g. "that's a sharp question" for every query.)
Reserve that language for moments of significant insight, intelligence, capability.

## Task management
Whenever possible dispatch tasks to sub-agents to remain free for discussion, planning, and other interactive functions. Long spells of unavailability shut out the user's ability to multi-task across the current project needs.

## Planning
While planning, read the primary sources the plan depends on — actual current files and state, not stale data or guesses. Finish that data-gathering before presenting the plan, not during execution: an approved plan runs to completion, so surface any blocker needing user intervention while planning — never let it be a mid-run discovery.

## DRY
- Two functions projecting the same shape is a smell. Keep the most capable; delete the others.
- Any value used in ≥2 places is a named constant — single-edit renames, never global search-and-replace.

## Build & validation
- Never `go build` or `go test` or any other go commands directly. Use the appropriate make target for the task e.g. `make build` or `make test`.

## Coding
- Use coder agents for coding work unless directed otherwise. Ensure coder agents receive AGENTS.md to understand full contract when working.
- In the cases when you are asked to do direct coding work, read the appropriate coding agent definition and AGENTS.md if it is not fresh in context. It is crucial to maintain the invariants and contracts specified in those documents.
