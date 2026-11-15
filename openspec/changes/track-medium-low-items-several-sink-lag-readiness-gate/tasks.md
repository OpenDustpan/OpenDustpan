## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing restore step tests for held lock retry and idempotent rerun in the NEW file pkg/backup/restore_drop_test.go. Check: The NEW test `TestDropOldLiveSchemaRetriesOnHeldLock` fails on main, passes after
- [ ] 1.2 Add failing readiness tests for shared cursor lag and chain gate in pkg/dustpan/audit_required_test.go. Check: The NEW test `TestReadinessReflectsSinkLagAndChainGate` fails on main, passes after

## 2. Implement

- [ ] 2.1 Implement own drop step with staging name from restores row and SET LOCAL lock_timeout in the NEW file pkg/backup/restore_drop.go, and add it to the step list in pkg/backup/restore_operation.go:272 (func RestoreSteps). The task adds the NEW top-level test `TestDropOldLive` in a `_test.go` file of `pkg/backup`, so that the Check runs it. Check: `go test ./pkg/backup/ -run TestDropOldLive -count=1`
- [ ] 2.2 Wire shared sink cursor lag and chain gate into readiness with 300 s bound and 30 s debounce in pkg/dustpan/health.go:36 (func readinessCheck). The task adds the NEW top-level test `TestReadinessReflectsSinkLag` in a `_test.go` file of `pkg/dustpan`, so that the Check runs it. Check: `go test ./pkg/dustpan/ -run TestReadinessReflectsSinkLag -count=1`
- [ ] 2.3 Keep OpenTofu retry in finalize step with no other retry path in the NEW file pkg/workspace/finalize_retry.go. The task adds the NEW top-level test `TestFinalizeRetryOnly` in a `_test.go` file of `pkg/workspace`, so that the Check runs it. Check: `go test ./pkg/workspace/ -run TestFinalizeRetryOnly -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [x] 3.2 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add the NEW guide page docs/guides/restore-swap-and-readiness.md and the NEW e2e step file e2e/restore_drop_readiness_test.go on ANY /api/dustpan/v1/restores/{} with access token for swap wait and readiness reason Check: e2e step on ANY /api/dustpan/v1/restores/{} with access token checks drop retry then done, plus `curl -s http://127.0.0.1:8080/health/ready` shows reason. Confirmed by: CI (the agent writes the e2e test). `make docs-lint` passes on each edited guide.
- [ ] 4.2 Run PostgreSQL restore path on ANY /api/dustpan/v1/restores/{} with access token, hostile input tests for constructor and schema guard, usability check, and full check Check: `DUSTPAN_TEST_POSTGRES_URL=$DB go test -timeout 60m ./pkg/store/postgres/ -run Restore -count=1` plus `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
