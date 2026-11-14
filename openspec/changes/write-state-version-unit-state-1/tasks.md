## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Regression tests

- [ ] 1.1 Test that a Pulumi checkpoint and an OpenTofu finalize each set the current version. Check: go test ./pkg/store/... -run TestStateWriteSetsCurrentVersion -count=1
- [ ] 1.2 Test that a final write sets the final version and closes an awaiting job. Check: go test ./pkg/store/... -run TestFinalWriteClosesAwaitingJob -count=1
- [ ] 1.3 Test a duplicate final write and a rollback. Check: go test ./pkg/store/... -run 'TestDuplicateFinalIdempotent|TestFinalWriteRollback' -count=1

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Check the callers that type-assert on `Store`: `internal/api/server/server.go`, `pkg/access/accesstest/accesstest.go`, `pkg/access/accesstest/caller_grants.go`, `pkg/access/accesstest/check.go`, `pkg/access/accesstest/fence.go`, `pkg/access/accesstest/holders.go`, `pkg/access/accesstest/moves.go`, `pkg/access/accesstest/review.go`. Check: the named files change in the same pull request
- [ ] 2.3 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Add the e2e step with the real Pulumi CLI and OpenTofu. Pulumi: after `pulumi up`, read GET /api/stacks/{namespace}/{project}/{stack}/export with a personal token of the Developer. OpenTofu: after `tofu apply`, read GET /api/v2/workspaces/{workspace_id}/current-state-version with a personal token of the Developer. Check: make e2e E2E_FOCUS=state-versions on SQLite and PostgreSQL
- [ ] 3.2 Add the docs page on state versions. Check: make docs-lint
- [ ] 3.3 Run the full check. Check: make check with zero findings
