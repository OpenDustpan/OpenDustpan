---
version: 3
---

Issue: #1255.

## Why

**Persona: operator.** Operator runs an installation where OpenTofu lock requests write one decision record per evaluation and no job prunes them, so disk use grows. The operator needs bounded retention that still keeps each record for audit.

The user path this change must make work:

1. **Docs page.** Operator reads the docs page for decision record retention and the audit retention setting.
2. **CLI.** Developer runs tofu plan and apply against the server; each state write evaluation writes one decision record.
3. **API.** Namespace administrator reads state.write decision records through the existing record read route and sees expired records disappear after the sweep.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| State write refused because the decision record store cannot take the write. | The server answers 503 with code gate.unavailable when the decision store cannot take the write; check server health with the status command and retry the state write. | `opendustpan status` |

Evidence for the claims above:

- The server builds the state.write gates and records their effective value at start. Proof: pkg/dustpan/stategates_start.go:58 (func Server.recordStateWriteGates) and pkg/dustpan/stateguard.go:94 (func newStateWriteGates), as the PM finding verified on main.
- Each OpenTofu lock request is one evaluation, and no job prunes the records. Proof: Issue 1255: each OpenTofu lock request is one evaluation, and -lock-timeout retries a lock many times. So the records grow with every lock request, and no job prunes them.
- A retry against a held lock writes no record. Proof: internal/api/tfe/locks.go:168 (func Handler.lockOnce) runs guardStateWrite with lockCheck on a workspace that was free at the last read; a held workspace answers 409 and asks no guard. Issue 1255 note: the TFE lock route asks the guard only when the lock is free.

## What Changes

- Keep one decision record for each state.write evaluation, including allow results with no gate.
- Prune state.write decision records after the audit retention in bounded batches ordered by stable record ID.
- Keep the held-lock behavior: a retry on a held workspace asks no guard and writes no record.

Out of scope:

- Change of the state.write gate set or fact schema.
- Change of the audit retention value itself.
- Console views for decision records.
- Prune of installation access-limit records.

## Usability

Where the path still costs the person time or surprises them:

- Operators see record growth as steady disk use with a visible prune count, not as an unbounded table.
- Every allowed write still has a record until retention passes, so auditors see no gap.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Each evaluation writes one decision record. (`pkg/gate/statewrite_test.go:606 (TestEachEvaluationWritesOneRecord)`)
- The server records the effective value of the state.write gates at start. (`pkg/dustpan/stategates_start.go:58 (func Server.recordStateWriteGates)`)
- The state.write gates are built here. (`pkg/dustpan/stateguard.go:94 (func newStateWriteGates)`)
- The TFE lock handler runs guardStateWrite with lockCheck before the placer and the lock call, on a workspace free at the last read; a held workspace answers 409 and asks no guard. (`internal/api/tfe/locks.go:168 (func Handler.lockOnce)`)
- The force-unlock handler reads the allowance first, then runs guardStateWrite with forceUnlockCheck before the override audit entry. (`internal/api/tfe/locks.go:352 (force-unlock handler)`)
- The stategate adapter guards the state.write decision point for HTTP adapters that write state. (`internal/api/stategate/stategate.go:30`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 3).

- Decisions that apply: 3 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "One decision record for each evaluation (owner). The record explains every block, every wait and every warn (ADR 229)."
- Effect: The change keeps one record for each evaluation, also when no gate exists; old records leave through the retention sweep.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 16).

- Decisions that apply: 16 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Effect: The change does not narrow the writes that meet state.write; it only prunes their records after retention.
- Conflict: none.

**ADR 229: Decision records, the simulate route and starter packs** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "A record of `state.write` or of an access limit lives for the audit retention (ADR 68)."
- Effect: The change keeps the audit-retention lifetime for state.write records and adds the sweep that enforces it; a prune batch entry names each deleted ID.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An unlocked state version on the TFE-compatible API (owner, 2026-11-07). Such a write meets `state.write` before its transaction."
- Effect: An unlocked-write refusal is a state.write outcome, so its record follows the same audit-retention prune.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add an e2e step: OpenTofu lock retry with -lock-timeout=30s against th" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- May an evaluation with no gate and no limit skip its decision record? No. ADR 222 decision 3 needs one record for each evaluation, owner-tagged. The volume fix is the retention sweep over time, not skipping records.
- How long does a state.write decision record live, including refusals and unlocked writes? ADR 229 decision 5: a record of state.write lives for the audit retention. The sweep deletes expired records, allow and refusal, including unlocked-write.
- Which audit retention value applies when the retention changes after a record is written? The sweep reads the current audit retention at sweep time. A shorter value makes old records eligible at the next sweep.
- May a namespace administrator prune installation access-limit records through this sweep? No. The sweep is a system background task with no caller principal and deletes only namespace-scoped state.write records.
- What cadence, batch size and stuck-step rule does the sweep use? Once per hour, 100 records per batch, 5 minute lease. The settings resource shows the effective values and the last prune count. A stuck open step becomes eligible 7 days after retention passes, and the prune entry marks it open-step.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
