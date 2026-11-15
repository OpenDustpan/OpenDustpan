## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Write the failing test for a failed apply with no new current state version in the NEW file pkg/audit/fanout_failure_test.go. Check: TestFanoutFailedApplyKeepsPriorState fails on main (calls ANY /api/v2/workspaces/{}/state-versions with an access token).
- [ ] 1.2 Write the failing test for the failed apply record in the NEW file pkg/audit/fanout_failure_test.go. Check: TestFanoutFailureRecordNamesWorkspace fails on main (calls GET /api/dustpan/v1/namespaces/{}/units with an access token).
- [ ] 1.3 Write the failing test for crash, cancel, secret redaction and lock release in the NEW file pkg/audit/fanout_failure_test.go. Check: TestFanoutCrashCancelAndLockRelease fails on main (calls ANY /api/v2/workspaces/{}/state-versions with an access token).

## 2. build

- [ ] 2.1 Wire the per-unit outcome keyed by workspace stable ID through the fan-out apply path in the NEW file pkg/audit/fanout_outcome.go. Check: ANY /api/v2/workspaces/{}/state-versions with an access token shows the prior current version for the failed workspace.
- [ ] 2.2 Wire the failure record with the workspace stable ID and the failure reason, without secret values, in the NEW file pkg/audit/fanout_outcome.go. Check: GET /api/dustpan/v1/namespaces/{}/units with an access token lists the failed unit.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 Write the Terragrunt failure page. Check: Each command on the page runs as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/failure_in_one_unit_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Run the usability check of the failure path. Check: A Developer finds the failed unit from the docs page alone and each error names one command. A person's step.
- [ ] 4.3 Run the e2e fan-out apply over three units with one failing unit. Check: BLOCKED by the NEW fan-out change order apply path; runs on PostgreSQL over TLS with the real binary.
- [ ] 4.4 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
