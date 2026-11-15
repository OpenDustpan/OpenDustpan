## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the failing test TestDrainReleasesBackupForTakeover in internal/servertest/drain_test.go (next to TestReadinessDuringDrain at :22): on SIGTERM the instance drains through WithDrainPeriod and the existing shutdown path, the backup worker releases its claim when Run returns (pkg/dustpan/dustpan.go:2094), and another instance takes the backup over and finishes it. No route: the test drives two test servers and the store only. Check: `go test ./internal/servertest/ -run TestDrainReleasesBackupForTakeover -count=1` fails on main only if the takeover fails; when it passes from the start, the pull request body says so and the task counts as done
- [ ] 1.2 Add the failing test TestReadiness503DuringDrainHasRetryAfter in pkg/dustpan/health_internal_test.go (next to TestReadinessDrains at :224): GET /health/ready during the drain answers 503 with the body of main (failed: draining) and a `Retry-After` header. Check: `go test ./pkg/dustpan/ -run TestReadiness503DuringDrainHasRetryAfter -count=1` fails on main (no Retry-After)

## 2. Build

- [ ] 2.1 Add the `Retry-After` header to the draining 503 of GET /health/ready in pkg/dustpan/health.go:338 (func readiness.writeDraining), with the value of the drain period in whole seconds (at most 15 s). Keep the constants of main: shutdownTimeout 30 s (the bound for in-flight requests and the claim release), DefaultDrainPeriod 10 s, maxStopTime 45 s (pkg/dustpan/dustpan.go:130-142), checked by checkDrainPeriod (pkg/dustpan/health.go:493). Add no drain timer and no new constant. Check: the test of task 1.2 passes
- [ ] 2.2 Prove the claim release of the existing shutdown path: no new code unless the test of task 1.1 fails; then fix the release in pkg/dustpan/dustpan.go (the backup worker, :2094) only. No route: the test drives two test servers and the store only. Check: `go test ./internal/servertest/ -run 'TestDrainReleasesBackupForTakeover|TestReadinessDuringDrain' -count=1` passes
- [ ] 2.3 Document the drain, the bounds and the claim takeover in the existing guide docs/guides/several-instances.md. Check: Docs page runs as written in review and states proxy timeouts exceed drain timeout. Confirmed by: CI (an e2e step in the NEW file e2e/rolling_deploy_graceful_drain_release_docs_test.go runs each command of the docs page against the e2e test server).

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Run e2e drain step against real CLI through proxy; the step calls route ANY /api/v2/state-versions through its authorization gate with access token credential. Check: Real CLI loop of `tofu apply` and `pulumi up` through proxy with no failed command during one instance drain.
- [ ] 4.2 Run wiring, route with authorization actions, docs page and usability check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
