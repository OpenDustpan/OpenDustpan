## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 BLOCKED by the open questions of the proposal and by the dependency to be written (a tool call that carries a delegation and a reason). Check: each answer is a repository record, and the dependency is on main.

## 1. Failing tests

- [ ] 1.1 In the NEW file `pkg/dustpan/agent_audit_test.go`, add `TestAgentStepWritesAuditEntry`: run one delegated step and read the delegation ID in the agent block and the reason in `Entry.Reason` from `GET /api/audit/entries`. Check: it fails on main. BLOCKED by open question 1.
- [ ] 1.2 In the same file, add `TestAgentDenialIsAudited`: a call outside the scope writes a denial entry. Check: it fails on main. BLOCKED by open questions 3 and 4.
- [ ] 1.3 In the same file, add `TestAgentChainVerifies`: write delegated entries, call `GET /api/audit/check`, tamper one entry and check that the check names its sequence number. Check: it fails on main.
- [ ] 1.4 In the same file, add `TestAgentAuditReadsFailFast`: block the database and check that the two routes answer 503 with `Retry-After: 5` and `audit.entry-read-unavailable`. Check: it fails on main.

## 2. Build

- [ ] 2.1 Write the delegation ID into the block and the reason into `Entry.Reason` on each delegated step, in the same transaction as an allowed step. BLOCKED by open question 2 for the block rule. Check: `GET /api/audit/entries` returns both for each delegated step.
- [ ] 2.2 Cover the field in the chain hash with no version (ADR 380 decision 3). Check: `GET /api/audit/check` verifies a chain that holds delegated entries.
- [ ] 2.3 SECURITY. Add the hostile tests of the layers table of `design.md`, and for each guard a test that fails when the guard is removed. Check: remove each guard in turn in a local run and see its test fail.
- [ ] 2.4 Document the fields in `docs/guides/audit-log.md` with one copy-paste read command. Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients when an answer changes. Never edit a generated file by hand. Check: the generators leave no diff.

## 4. Definition of done

- [ ] 4.1 Extend `e2e/audit_export_test.go` on PostgreSQL through the real binary over TLS: delegate, run one agent step, list `GET /api/audit/entries` and call `GET /api/audit/check`. Check: the step passes in CI. BLOCKED by the Dusty run job.
- [ ] 4.3 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
