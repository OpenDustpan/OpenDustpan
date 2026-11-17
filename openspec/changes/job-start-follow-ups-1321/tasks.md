## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Write the failing test TestClaimPollCountsBoundSkips in the NEW file pkg/job/claimer_skips_test.go (package job_test): a fake ClaimObserver gets the number of operations that one Claim skips past the bound `maxSkippedClaims` (pkg/job/claimer.go:30). Check: `go test ./pkg/job -run TestClaimPollCountsBoundSkips -count=1` fails on main

## 2. Fix

- [x] 2.1 Correct the Route.IdempotencyKey comment in internal/api/registry/operations.go:14 and in the route files of the operations packages: internal/api/registry/routes.go, internal/api/profile/routes.go, internal/api/profile/operations.go, internal/api/notifications/routes.go and internal/api/notifications/operations.go. Check: rg -n -F 'Route.IdempotencyKey' internal shows the fixed text
- [x] 2.2 Count and log the operations skipped by the bound where the bound is, in pkg/job/claimer.go (DurableClaimer.Claim, :137-166): add the NEW port `job.ClaimObserver` (`interface{ SkippedClaims(n int) }`) and the NEW no-op `job.NoopClaimObserver`, the default; the NEW option `job.WithClaimObserver` sets it, and a nil observer is refused (the no-op stays), as WithClaimerLogger does for a nil logger. The log line uses the claimer's injected slog logger (the option WithClaimerLogger sets it). pkg/job imports no metrics code (depguard). Check: `go test ./pkg/job/... -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Confirm that no request flow changed, so the round-trip budgets stand: the change adds no route and no caller of a route. Check: the round-trip tests in `internal/servertest/roundtrips_test.go` pass unchanged. Confirmed by: CI (the round-trip tests pass unchanged).
- [x] 3.2 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [x] 4.1 Wire the metric in the wiring layer, which owns the metrics package: the NEW counter dustpan_job_claim_poll_skipped_total in internal/metrics/server.go, passed to the claimer through `job.WithClaimObserver` in pkg/dustpan/builtin_stores.go:372 (the test server and the e2e claimer keep the no-op observer). Write the wiring test TestClaimSkipsMoveTheMetric in the NEW file pkg/dustpan/claim_observer_internal_test.go. No route: the test reads the metrics registry only. Check: TestClaimSkipsMoveTheMetric passes and the metrics route lists the counter
- [x] 4.2 No e2e step (PM decision, rule 16): the skip counter lives in the durable claimer (pkg/job/claimer.go), which no route serves, so there is no route or credential to name. The proof is TestClaimPollCountsBoundSkips (task 1.1) and the NEW metric dustpan_job_claim_poll_skipped_total in internal/metrics/server.go, which task 4.1 shows on the metrics route. Check: `go test -timeout 60m ./pkg/job -run TestClaimPollCountsBoundSkips` passes. Confirmed by: CI (the agent writes the e2e test).
- [x] 4.3 Docs page note and usability check from the docs page alone Check: make docs-lint. A person's step.
- [ ] 4.4 All checks pass Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
