# ROADMAP.md — LaTerm

Future intent. **Outside the contract-document precedence chain** (SPEC.md >
ARCHITECTURE.md > CONVENTIONS.md > code): read this for planning, never as a
specification. Nothing here is a requirement, and nothing here describes how
laterm behaves today — the contract documents own the now.

An item leaving this file means it either landed (and the contract documents
now state it as present-tense fact) or was abandoned.

---

## Next Steps — code

Code tasking only. As of 2026-08-29 the contract documents are caught up to the
code and verified against it; what remains below is work on the **code**, where
it diverges from a design ARCHITECTURE.md states correctly. Do not resolve any
of these by editing the documents.

### 0. `Makefile` - port to `justfile` and change project to use `just`

we are only using it as a command alias phone book. That's textbook `just` territory.

### 1. `main` names a specific graphics protocol

`src/main.rs:256` — `let opaque_bg = proto.name == "sixel";`

Violates ARCHITECTURE §1.5 / §5.5 ("nothing outside `graphics` names a
protocol"). The public `name` field on `Protocol` exists only to enable this;
ARCHITECTURE deliberately does not document it, because documenting it would
ratify the breach.

Direction: expose the property itself on `Protocol` — e.g.
`opaque_background() -> bool` — so `main` stays protocol-blind, and drop or
privatize `name` if nothing else needs it.

### 2. `main` hosts the settings-merge subsystem

`src/main.rs` is 917 lines and contains `merge_hooks`, `entry_has_command`,
`settings_file`, and `install_hooks` (~140 lines) alongside `catch_up` and
`within_window`, against §3's "wiring only … no parsing logic."

Direction: extract an `install` module. Note that §9 *does* sanction
`serde_json` in `main` for the merge, so ARCHITECTURE contradicts itself here —
moving the code resolves the contradiction in the right direction.

### 3. Help text omits accepted flags

`src/main.rs:33` (`USAGE`) omits `-h`, which `parse_args` accepts
(`src/main.rs:115`), and omits the `=`-joined forms (`--hook=`, `--cwd=`,
`--log=`), which are accepted and are now required by SPEC `R-9.1`.

Direction: add both to the help text.

### 4. `mathscan::Segment` is a tagged struct, not an enum

`struct Segment { kind: Kind, text: String, display: bool }`, where `display` is
documented as unused when `kind` is `Text` — a shape an enum with payload would
make impossible. Git history confirms it was never an enum, so this is original
design, not drift; ARCHITECTURE now describes the struct as it actually is.

Direction (optional, idiomatic-Rust cleanup, no behavior change): convert to
`enum Segment { Text(String), Math { expr: String, display: bool } }` and update
ARCHITECTURE §3 to match.

---

## Committed

Intent is settled; only scheduling is open.

### Windows live-hook path

`ipc` is a Unix-domain socket, so `--hook`, `--install-hooks`, and live
rendering do not work on Windows (ARCHITECTURE.md §11.1). The intended
implementation is a named pipe.

- The module is already platform-split in the same shape `termbg` uses, so the
  change is expected to be confined to `ipc`.
- The transport is expected to need no new dependency: `windows-sys` is already
  a Windows-target dependency for `termbg` and `logging`.
- Paste, graphics protocol selection (Sixel via DA1), and `termbg` are already
  at full Windows parity and are unaffected.

Until this lands, SPEC.md `R-2` is satisfied on unix only.

---

## Wanted

Worth doing; no commitment and no schedule.

### Proportional row scaling for Sixel

`sixel::encode` accepts `rows` and discards it, so on a Sixel terminal math does
not track the terminal's text size — a partial shortfall against SPEC.md `R-7.2`
(ARCHITECTURE.md §11.2). Closing it means resampling in the encoder rather than
only padding up to band and cell multiples.

### Fuzz targets for the pure parsers

`mathscan`, `hook`, and `convo` import nothing of laterm and touch no I/O, which
makes them the cheapest high-value fuzz surface in the crate (ARCHITECTURE.md
§10). Rust fuzz targets for all three would be a good addition.

---

## Sequencing guidance

Not a task list — the order in which requirement coverage buys the most
confidence, for whoever is planning implementation or test work:

1. **Process model, startup, and the live transport** — `R-1`, `R-2`, `R-8.1`,
   `R-9`, `R-10`, `R-11`. Nothing else is reachable until a turn arrives in a
   window.
2. **Parsing** — `R-5` (math scanning), `R-2.7` / `R-3.4` (payload and
   transcript parsing). Pure, deterministic, and testable without a terminal:
   the cheapest place to buy confidence.
3. **Rendering and output format** — `R-6`, `R-7`, `R-8.2`–`R-8.5`.
4. **Auxiliary paths** — `R-3` (catch-up), `R-4` (paste), `R-12` (logging),
   `R-13`.
