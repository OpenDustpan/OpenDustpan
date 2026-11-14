## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the NEW test `TestMissingNamespaceDisposition` in pkg/job/jobtest/claimer.go for confirmed missing versus transient namespace disposition, with and without cancellation Check: NEW `go test ./pkg/job -run TestMissingNamespaceDisposition -count=1` fails on main
- [ ] 1.2 Add the NEW test `TestJobTokenStartRefusal` for job-token start policy refusal and an ordinary missing grant Check: NEW `go test ./pkg/job -run TestJobTokenStartRefusal -count=1` fails on main
- [ ] 1.3 Add the NEW test `TestChunkNamespaceReuse` as an instrumented test for namespace lookup count, retries, separate namespaces and mid-claim delete Check: NEW `go test ./pkg/job -run TestChunkNamespaceReuse -count=1` fails on main
- [ ] 1.4 Add the NEW test `TestPlanPartSecrecy` for plan-part secrecy through PutPlan and CheckPlanBody including reservation restore and a legacy body Check: NEW `go test ./pkg/job -run TestPlanPartSecrecy -count=1` fails on main
- [ ] 1.5 Add the NEW test `TestPlanPartSecrecyStore` for plan-part secrecy through the jobstore write path Check: NEW `go test ./pkg/store/internal/jobstore -run TestPlanPartSecrecyStore -count=1` fails on main
- [ ] 1.6 Add the NEW test `TestDriftStepAllowlist` for tool-scoped drift-step allowlists including empty and mixed lists Check: NEW `go test ./pkg/runner -run TestDriftStepAllowlist -count=1` fails on main
- [ ] 1.7 Add the NEW test `TestExtendLease` lease case with a 3 to 5 second initial lease and unchanged expiryWait on sqlite and postgres, in the file pkg/durable/durabletest/durabletest.go Check: NEW `go test ./pkg/durable/durabletest -run TestExtendLease -count=1` fails on main on sqlite and postgres

## 2. Fixes

- [ ] 2.1 Update the cancel comment in pkg/job/store.go and pkg/job/claimer.go to distinguish the ended operation from the failed job and agree with testExpiryDuringCancel Check: `rg -n 'canceled operation' pkg/job/store.go pkg/job/claimer.go` shows the updated comment
- [ ] 2.2 Fix the named sites and keep retries, isolation and error matching Check: `go test ./pkg/job ./pkg/runner ./pkg/store/internal/jobstore -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the full check gate with zero findings Check: `make check` passes. Confirmed by: CI (make check).
