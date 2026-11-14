Issue: #1255.

## Why

**Persona: operator.** Operator runs an installation where OpenTofu lock retries write one decision record per lock request with no prune, so disk use grows. The operator needs bounded retention that still keeps each record for audit.

The user path this change must make work:

1. **Docs page.** Operator reads the docs page for decision record retention and the audit retention setting.
2. **CLI.** Developer runs tofu plan and apply against the server; each state write writes one decision record.
3. **API.** Namespace administrator lists state.write decision records through the tool-neutral API and sees expired records disappear after the sweep.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| State write refused because the decision record store cannot take the write. | The server answers 503 with code gate.unavailable when the decision store cannot take the write; run the retention sweep and retry the state write. | `opendustpan gates records prune --point state.write` |

Evidence for the claims above:

- The server evaluates state.write gates around state writes. Proof: pkg/dustpan/stategates_start.go:15:// stateWriteGatesKey is the key of the effective value of the state.write
- Each OpenTofu lock request was one evaluation and lock retries repeat it. Proof: Part of #899. Follows #1032. Each OpenTofu lock request is one evaluation, and `-lock-timeout` retries a lock many times. So the records grow with every lock request, and no job prunes them.
- A retry against a held lock writes no record because the route asks the guard only when the lock is free. Proof: Note: the TFE lock route now asks the guard only when the lock is free, so a retry against a held lock writes no record.

## What Changes

- Keep one decision record for each state.write evaluation, including allow results with no gate.
- Prune state.write decision records after the audit retention in bounded batches ordered by stable record ID.
- Keep the lock-held fast path: retries against a held lock write no record because no evaluation runs.

Out of scope:

- Change of the state.write gate set or fact schema.
- Change of the audit retention value itself.
- Console views for decision records.
- Prune of installation access-limit records.

## Usability

Where the path still costs the person time or surprises them:

- Operators see record growth only as steady disk use with a visible prune count, not as an unbounded table.
- The no-record case never surprises auditors: every allowed write still has a record until retention passes.

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
- Effect: The change keeps the audit-retention lifetime for state.write records and adds the sweep that enforces it; a prune batch entry names each deleted ID and the step entry keeps its ID.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An unlocked state version on the TFE-compatible API (owner, 2026-11-07). Such a write meets `state.write` before its transaction."
- Effect: An unlocked-write refusal is a state.write gate outcome, so its record follows the same audit-retention prune as each state.write record.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (failure mode): proposal.md and design.md: the error row says a full decision store returns 409 gate.unavailable, but ADR 230 decision 4 maps gate.ErrUnavailable to 503 gate.unavailable; the status and the retry behaviour are not specified in the spec. Resolution: answered: Error row uses 503 gate.unavailable per ADR 230 decision 4 and the sweep retry command.
- R1.2 (adr conflict): proposal.md ADR check: the change writes a record for every allow with no gate, which ADR 222 decision 3 (owner) requires; the change also prunes records that ADR 229 decision 5 keeps for the audit retention. The chain of records and the audit entry of each prune batch must not drop the record ID named by the audit entry of the step; the spec does not say what the audit entry of a step keeps after its record is pruned. Resolution: answered: Design decision 3 and the audit-reference requirement keep the step audit entry record ID after prune; prune batch entry names each deleted ID.
- R1.3 (missing scenario): specs/gate-decision-records/spec.md: no scenario covers a refusal record (written in its own transaction under ADR 229 decision 5) reaching the retention sweep, nor a record of an unlocked-write under ADR 335 decision 3. Resolution: answered: Added scenarios Expired refusal record is pruned and Expired unlocked-write record is pruned to the Retain state.write records requirement; the prune audit entry names each deleted ID while the step entry keeps it.
- R1.4 (open question): design.md Decisions 2: the sweep deletes expired records, but the design does not say how a retention change applies to records already written, or whether the audit retention is read at sweep time or at write time. Resolution: answered: Design decision 2 reads audit retention at sweep time and applies it to records already written.
- R1.5 (failure mode): design.md Structure and Risks: a backfill of old records makes the first sweep slow, but bounded batches, restart in the middle of a batch, and two sweepers running at once (multi-replica workers) are not covered by any scenario or test. Resolution: answered: Sweep-safety requirement covers bounded batches, restart mid-batch, and one holder through a store lease.
- R1.6 (failure mode): design.md Risks: a lock retry storm still writes one record per free-lock attempt; no rate limit or cap on record writes per change is specified, so the disk growth the item fixes is only bounded by the sweep cadence. Resolution: answered: Risks and design state the bound is over time by sweep cadence and batch size with no per-change cap.
- R1.7 (wrong claim): proposal.md ADR check: ADR 222 decision 3 is marked owner-tagged and its text is quoted, but ADR 222 is cited as 'owner-tagged: decision 3' while the quote in the file carries the same '(owner)' tag only via the decision; the 'Effect' line says volume is controlled by the lock-held fast path, which the input does not show is a fact of the server code. Resolution: answered: ADR 222 effect no longer claims the fast path as server code; the fast path cites the issue note in evidence.
- R1.8 (missing scenario): specs/gate-decision-records/spec.md: the lock-held fast path is in the proposal but has no requirement or scenario; tasks 2.2 only checks it through make e2e. Resolution: answered: Added scenario Retry against held lock writes nothing to the Held lock writes no record requirement; the server runs no evaluation and writes no record when the lock is held.
- R1.9 (open question): proposal.md Open questions says none, but the item asks to decide whether an evaluation with no gate writes a record; the answer rests on ADR 222 decision 3 and is accepted here without an owner review, and the 'Questions considered' section gives no proof that the fast path is in code (the TFE route statement has no rg proof). Resolution: answered: Considered answers rest on ADR 222 decision 3 and ADR 229 decision 5 with the issue-note proof for the held-lock path.
- R1.10 (scope): tasks.md 2.2: the sweep is wired to the server worker and the e2e test is listed, but the docs page and the route's authorization actions are only in section 3 without a named action for the prune operation. Resolution: answered: Sweep runs as a system task with no caller grant; namespace manage action does not prune installation records.
- R2.1 (owner decision conflict): proposal.md and design.md claim the no-gate allow question is answered by ADR 222 decision 3 alone, but the owner decision of 13 Nov (queue) binding for the unlocked-write and refusal prune is not cited; the change prunes refusal records under ADR 229 decision 5 while ADR 335 decision 3 (owner, 2026-11-07) makes the unlocked-write refusal a gate outcome whose record retention is not decided by the change. Resolution: answered: Added ADR 335 to adr_check; unlocked-write refusals prune under ADR 229 decision 5 as state.write records.
- R2.2 (adr conflict): The spec requirement 'Retain state.write records for audit retention' says the sweep deletes records after the audit retention, but ADR 229 decision 5 ties the lifetime to the job or change order it explains (ADR 198) for other records; the change does not state how a state.write record whose explained step is still open is protected from the sweep. Resolution: answered: Retention requirement now keeps records whose explained step is still open; sweep deletes only completed steps past retention.
- R2.3 (failure mode): No scenario covers the sweep deleting a record while the audit entry of the step is written in the same transaction; the 'Prune keeps the audit reference' requirement does not say what happens if the step audit entry is missing or the store lease expires mid-batch. Resolution: answered: Audit-reference requirement keeps records with a missing step audit entry and makes lease expiry mid-batch restart-safe.
- R2.4 (wrong claim): The ADR check says ADR 222 decision 3 is owner-tagged and quotes it as current, but the Effect says volume is bounded by the retention sweep; the sweep cadence and batch size give no bound on writes per lock retry, so the claimed bound is unsupported by any requirement. Resolution: answered: Effects and risks state the sweep bounds growth over time only, with no per-retry cap.
- R2.5 (missing scenario): The error row names gate.unavailable with 503 and tells the operator to run the prune command, but no scenario covers the state write retry after the prune, nor what the state write does while the sweep holds the lease. Resolution: answered: Added scenarios for a new write after prune and for writes that proceed while the sweep holds its lease.
- R2.6 (open question): The authorization action for the prune operation is namespace:manage-gates on the namespace object, but the sweep deletes records across namespaces in one batch; ADR 230 decision 2 does not say whether a record of an installation access limit (ADR 229 decision 5) is pruned by a namespace administrator. Resolution: answered: Considered and out_of_scope state the sweep is a system task that deletes only namespace state.write records.
- R2.7 (open question): The audit retention is read at sweep time, but ADR 229 decision 5 says the record lives for the audit retention (ADR 68) with no statement of which value applies when the retention is shortened below a record's age during an open change; the design does not decide this. Resolution: answered: Considered and design decide the sweep reads the current retention at sweep time for records already written.
- R2.8 (failure mode): The lock-held fast path is cited only to an issue note with no rg proof in the code; if the TFE route asks the guard before the lock check, a held-lock retry still writes a record, and no test in tasks.md checks the server code path. Resolution: answered: Evidence keeps the issue note as the only proof for the held-lock path and tasks verify the TFE route guard order with rg plus a held-lock test.

## Open questions

none

Questions considered and answered:

- May an evaluation with no gate and no limit skip its decision record? No. ADR 222 decision 3 needs one record for each evaluation, owner-tagged. The volume fix is the retention sweep over time, not skipping records and not a per-retry cap.
- How long does a state.write decision record live, including refusals and unlocked writes? ADR 229 decision 5 sets it: a record of state.write lives for the audit retention. The sweep deletes expired records, allow and refusal, including unlocked-write and the refusal in its own transaction; the audit entry of the step keeps the record ID after the prune.
- Which audit retention value applies when the retention changes after a record is written? The sweep reads the current audit retention at sweep time and applies it to records already written. A shorter retention makes old records eligible at the next sweep; a longer retention keeps them until the new period passes.
- May a namespace administrator prune installation access-limit records through this sweep? No. The sweep is a system background task with no caller principal. It deletes only namespace-scoped state.write records. A namespace administrator never prunes installation access-limit records through this sweep.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
