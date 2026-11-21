## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

Tests that main already passes are regression checks. They pass on main and after the change, because the change `audit-agent-block` landed that behavior. Task 0.1 confirms each one on the current main.

- [x] 1.1 Add a failing test in the NEW file `pkg/job/agent_audit_test.go`: the approval entry of an agent (`pkg/job/service_approve.go:424`) holds the agent in the agent block and holds no agent change key. Check: TestApprovalEntryAgentInBlock fails on main, passes after fix. If the fix of issue #3271 (an agent cannot reject) has landed and no code path writes an agent on the approval entry, drop this task and task 2.1 and state that in the pull request body.
- [x] 1.2 Regression check, no new test: `TestStartWritesAgentBlock` (`pkg/job/service_test.go:1124`) and `TestComputeHashAgentBlock` (`pkg/audit/agent_block_test.go:52`) pass on main and after the change. Check: both pass in `go test ./pkg/job/... ./pkg/audit/...`.
- [x] 1.3 Add a regression test in `pkg/audit/audittest/chain.go`: one changed byte of the agent block refuses the chain. Check: TestAuditTamperedBlockFailsCheck passes on main and after the change. Add it only if no existing test covers a changed block byte; task 0.1 records which.

## 2. Build

- [x] 2.1 In `pkg/job/service_approve.go:424`, remove the agent change key and write the agent block in the same transaction as the approval entry. Check: an approval entry with an agent ref, read with GET /api/audit/entries and an access token, shows the block and no change key.
- [x] 2.2 Document the agent block of a job entry on `docs/guides/audit-log.md`, with one example; the field of the entry read API comes from the change audit-agent-block. Check: GET /api/audit/entries with an access token returns the block of a job start entry; the docs page shows the example. `make docs-lint` passes on each edited guide.

The start path (`pkg/job/service_start.go`) already writes the block on main. Tasks 2.3 to 2.6 check the guards of that write and of the approval write of task 2.1. A test of 2.5 or 2.6 that already passes on main is a regression test; keep it, and say so in the pull request body.

- [x] 2.3 SECURITY. Build the service ref validation in `pkg/job/service_start.go:623`: refuse a malformed agent ref before any write and pass only the supplied ref and digest to the block writer. Check: Job start with a malformed agent ref is refused and GET /api/audit/entries with an access token shows no new entry.
- [x] 2.4 SECURITY. Build the same-transaction write in `pkg/job/service_start.go:623`: write the block and the entry in one transaction and roll back the job change when the block write fails. Check: Job start with a failed block write fails and GET /api/audit/entries with an access token shows no orphan entry.
- [x] 2.5 SECURITY. Add the hostile test for the service layer in the NEW file `pkg/job/agent_audit_test.go`: start a job with a malformed agent ref and check that the start is refused and no entry is written. Check: TestStartJobMalformedAgentRefRefused passes after fix.
- [x] 2.6 SECURITY. Add the hostile test for the port adapter layer in the NEW file `pkg/job/agent_audit_test.go`: force the block write to fail and check that the start fails, the job change rolls back and no orphan entry remains. Add the matching case to the job store conformance suite (`pkg/job/jobtest/create.go`, `pkg/job/jobtest/jobtest.go`, and the validation of entries in `pkg/job/jobtest/mem_test.go`): a create whose block entry is refused stores no job and no entry. Check: TestStartJobBlockWriteFailureRollsBack passes after fix, and the conformance case "a create whose block entry is refused stores no job and no entry" passes.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [x] 4.1 Run e2e audit read and chain verification over new and legacy entries. Check: GET /api/audit/check with an access token verifies the chain across both shapes.
- [x] 4.3 Run full wiring, docs page and make check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
