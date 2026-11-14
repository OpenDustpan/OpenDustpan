---
version: 1
---

Issue: #2324.

## Problem

A namespace administrator writes a gate that blocks removal of a resource, but for each write except restore of a state version the facts stay empty, so the gate never matches and nothing shows that.

## Evidence

- The state facts state.available, state.resources and state.removed are named in the gate code. Proof: pkg/gate/statechange.go:42
- The TFE-compatible handler builds the state facts for the restore of a state version. Proof: internal/api/tfe/states.go:802
- The stategate package notes writes where the server holds no new state. Proof: internal/api/stategate/stategate.go:202
- The guide lists the facts of each write. Proof: docs/guides/state-write-gates.md

## Fix boundary

- Fill state.available, state.resources and state.removed for the unlocked write, with instance counts by address.
- Fail closed with a catalog code and an audit entry when the state cannot be read, is over a limit, or is a fake envelope, and recheck the current state in the write transaction.
- Refuse a new gate save that reads a filled fact without state.available, and keep stored gates as they are.
- Update the matrix row in docs/guides/state-write-gates.md.

Out of scope:

- Guarding Pulumi import and restore at state.write (#2266) and filling their facts
- Lock, force-unlock and delete writes, which hold no new state
- Evaluation-time error for an unavailable fact group

## Check

- Write failing tests for the unlocked-write fill, the first write and the valid envelope in the NEW file pkg/gate/statechange_unlocked_test.go. Check: go test ./pkg/gate -run TestUnlockedWriteFillsStateFacts fails on main
- Write failing hostile tests for unreadable, over-limit and fake-envelope states in the NEW file internal/api/tfe/state_unlocked_test.go. Check: go test ./internal/api/tfe -run TestUnlockedWriteFailsClosed fails on main
- Write failing tests for the save-time refusal in the NEW file pkg/gate/gate_save_test.go. Check: go test ./pkg/gate -run TestSaveRefusesUnfilledRead fails on main
- Edit internal/api/tfe/states.go:602 (func Handler.writeStateVersion) to fill gate.StateChange for the unlocked write with the version recheck and the NEW codes state.unreadable, state.over-limit, state.invalid-envelope and state.changed, after gate-subject-job-token-has and state-facts-follow-ups-one. BLOCKED by the owner decision in RFC 119. Check: The tests of the first section pass
- Add an e2e step in the NEW file e2e/state_write_gates_test.go: a real OpenTofu unlocked write with an access token that holds the state.write grant, through Handler.writeStateVersion. BLOCKED by the owner decision in RFC 119. A person's step. Check: make e2e
- Update the matrix of docs/guides/state-write-gates.md and run the usability check from that page. A person's step. Check: make docs-lint
- Run the full checks. Confirmed by: CI (make check) Check: make check with zero findings

Depends on:

- gate-subject-job-token-has
- state-facts-follow-ups-one
- `gate-subject-job-token-has` (order of the milestone review)
- `state-facts-follow-ups-one` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- gate.StateChange carries the facts state.resources and state.removed. (`pkg/gate/statechange.go:30 (type StateChange)`)
- The TFE-compatible handler restores a state version and fills the facts. (`internal/api/tfe/states.go:434 (func Handler.restoreStateVersion)`)
- The TFE-compatible handler writes a state version. (`internal/api/tfe/states.go:602 (func Handler.writeStateVersion)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which writes fill the state facts, and is a gate that reads an unfilled fact in error?
   - Options: A. Do nothing more; keep the matrix in the guide; B. At evaluation, a gate that reads an unavailable fact is in error; C. At save, refuse a gate that reads state.resources or state.removed without state.available; D. B and C together, plus fill the unlocked write.
   - Provisional decision (owner review 14 Nov): Fill the unlocked write and refuse at save (C); leave evaluation-time error to the owner.
   - Reason: This removes the silent miss for the unlocked write and makes the rest visible, without failing writes that pass today. Benchmark: AWS IAM Access Analyzer
   - Persona impact: Option B fails writes that pass today for a namespace administrator. Escape hatch: A gate that reads state.available first, or onError skip.

Questions considered and answered:

- Which ADRs govern the fill? The pack names no ADR text. RFC 119 is discussing and is not a decision, so the owner choice stays an open question and no accepted ADR is touched.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
