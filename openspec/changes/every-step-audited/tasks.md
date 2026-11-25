## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 BLOCKED by the open questions of the proposal and by the Dusty run job (a tool call that carries a delegation and a reason; no change names it yet). Check: each answer is a repository record, and the dependency is on main.

## 1. Failing tests

- [ ] 1.1 In the NEW file `pkg/dustpan/agent_audit_test.go`, add `TestAgentStepWritesAuditEntry`: run one delegated step and read the delegation ID in the agent block and the reason in `Entry.Reason` from `GET /api/audit/entries` (`internal/api/audit/routes.go:49`). Check: it fails on main (NEW symbol `TestAgentStepWritesAuditEntry`; no code on main writes a delegated step). BLOCKED by open question 1.
- [ ] 1.2 In the NEW file `pkg/dustpan/agent_audit_test.go` (task 1.1), add `TestAgentDenialIsAudited`: a call outside the scope writes a denial entry. Check: it fails on main (NEW symbol `TestAgentDenialIsAudited`). BLOCKED by open questions 3 and 4.
- [ ] 1.3 In the NEW file `pkg/dustpan/agent_audit_test.go` (task 1.1), add `TestAgentChainVerifies`: write delegated entries, call `GET /api/audit/check` (`internal/api/audit/routes.go:51`), tamper one entry with `pkg/audit/audittest/chain.go` and check that the check names its sequence number. Check: it fails on main (NEW symbol `TestAgentChainVerifies`).
- [ ] 1.4 In the NEW file `pkg/dustpan/agent_audit_test.go` (task 1.1), add `TestAgentAuditReadsFailFast`: block the database and check that the routes `internal/api/audit/routes.go:49` and `:51` answer 503 with `Retry-After: 5` and `audit.entry-read-unavailable`. Check: it fails on main (NEW symbol `TestAgentAuditReadsFailFast`).

## 2. Build

- [ ] 2.1 Write the delegation ID into the block and the reason into `Entry.Reason` on each delegated step, in the same transaction as an allowed step. BLOCKED by the Dusty run job (no code on main writes a delegated step, so this task names no file yet) and by open question 2 for the block rule. Check: `GET /api/audit/entries` returns both for each delegated step.
- [x] 2.2 Already done on main: `pkg/audit/audit.go:640` (`hashWithChanges`) hashes `DelegationID` and `Reason` with no version, and `pkg/audit/agent_block_test.go:52` and `:139` test it. No new code. The chain check test is `TestAgentChainVerifies` in task 1.3 (`pkg/audit/audittest/chain.go`). Check: `GET /api/audit/check` verifies a chain that holds delegated entries. Confirmed by: task 1.3.
- [ ] 2.3 SECURITY. Add the hostile tests of the layers table of `design.md`: service in the NEW file `pkg/dustpan/agent_audit_test.go`, port adapter in `pkg/audit/agent_block_test.go` and `pkg/audit/audittest/chain.go`, HTTP handler in `internal/api/audit/audit_test.go`. For each guard add a test that fails when the guard is removed. Check: remove each guard in turn in a local run and see its test fail.
- [ ] 2.4 Document the fields in `docs/guides/audit-log.md` with one copy-paste read command. Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients when an answer changes. Never edit a generated file by hand. Check: the generators leave no diff.

## 4. Definition of done

- [ ] 4.1 Extend `e2e/audit_export_test.go` on PostgreSQL through the real binary over TLS: delegate, run one agent step, list `GET /api/audit/entries` and call `GET /api/audit/check`. Check: the step passes in CI (NEW step function `checkAgentAudit` in `e2e/audit_export_test.go`, next to `checkAuditExport` at :88; `docs/guides/audit-log.md` is the guide). BLOCKED by the Dusty run job. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Agent pre-run (human run-through in person-steps (every-step-audited 4.2)): Run a usability check of the audit read path. Check: the persona completes the path from the docs page alone and each error names the one command that helps. Record the transcript in the pull request body.
- [ ] 4.3 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
