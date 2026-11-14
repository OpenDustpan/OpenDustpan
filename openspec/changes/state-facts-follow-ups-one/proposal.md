---
version: 1
---

Issue: #2331.

## Problem

An upload accepts a look-alike encrypted document that the restore then refuses. A duplicate restore across two instances answers 409 where the idempotent answer may be correct. A 30 s wall-clock assertion in a test can flake under load.

## Evidence

- Main has the loose check IsClientEncrypted. Proof: pkg/workspace/tofu_state.go:143 (func IsClientEncrypted)
- Main has the duplicate restore helper. Proof: pkg/workspace/service.go:1873 (func Service.answerDuplicateRestore)
- Main has stateUnreadable and gate.Code. Proof: internal/api/stategate/stategate.go:377 (func stateUnreadable)
- Main has TestRollbackChangeLimit. Proof: pkg/workspace/rollback_change_internal_test.go:71

## Fix boundary

- Make the strict five-member envelope check the one check for new writes.
- Keep stored look-alikes readable.
- Delete the redundant ErrLimit branch in stateUnreadable.
- Replace the 30 s wall-clock assertion in TestRollbackChangeLimit with an attempt bound.

Out of scope:

- Pulumi state.write guard paths tracked in #2266.
- The duplicate restore answer until the PM decides.

## Check

- Add strict-envelope cases to TestClientEncryptionPathsAgree in pkg/workspace/rollback_change_internal_test.go:112 Check: `go test ./pkg/workspace/ -run TestClientEncryptionPathsAgree -count=1` fails on main
- Add a failing over-limit refusal test in NEW internal/api/stategate/limit_test.go Check: `go test ./internal/api/stategate/ -run TestOverLimitStateGivesGateLimit -count=1`
- Make IsClientEncrypted strict in pkg/workspace/tofu_state.go:143 and add the loose stored reader there Check: `go test ./pkg/workspace/ -run TestClientEncryptionPathsAgree -count=1` passes
- Delete the ErrLimit branch in internal/api/stategate/stategate.go:377 (func stateUnreadable) Check: `go test ./internal/api/stategate/ -run TestOverLimitStateGivesGateLimit -count=1` passes
- Replace the 30 s assertion in pkg/workspace/rollback_change_internal_test.go:71 with an attempt bound Check: `go test ./pkg/workspace/ -run TestRollbackChangeLimit -count=20` passes under load
- Run an e2e step with real OpenTofu: upload and restore through ANY /api/v2/state-versions/{}/actions/restore with an access token — Confirmed by: CI (make e2e) Check: `make e2e`
- Update the restore docs page, run the Developer path check and run make check — Confirmed by: CI (make check) Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main reports client-encrypted state through IsClientEncrypted. (`pkg/workspace/tofu_state.go:143 (func IsClientEncrypted)`)
- Main answers a duplicate restore through answerDuplicateRestore. (`pkg/workspace/service.go:1873 (func Service.answerDuplicateRestore)`)
- Main maps unreadable state in stateUnreadable. (`internal/api/stategate/stategate.go:377 (func stateUnreadable)`)
- Main ranks refusal codes in gate.Code. (`pkg/gate/gate.go:119 (func Code)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which duplicate-restore answer is correct: idempotent replay with 200 or 409?
   - Options: Answer 200 with the stored version and write no second version; Keep 409 for a duplicate restore.
   - Provisional decision (owner review 14 Nov): Answer 200 with the stored version and write no second version.
   - Reason: A retry then gets one stable answer and no second version exists. Benchmark: AWS: idempotent write APIs return the same stored result for a repeated request.
   - Persona impact: Developer sees one stable answer on retry; no persona is blocked. Escape hatch: `tofu state pull` reads the current state under either answer.

Questions considered and answered:

- Should upload and restore use one encryption check? Yes for new writes. Stored look-alikes stay readable so no stored state becomes unreadable.
- Does any ADR answer these follow-ups? No ADR is in the pack. No ADR conflict is claimed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
