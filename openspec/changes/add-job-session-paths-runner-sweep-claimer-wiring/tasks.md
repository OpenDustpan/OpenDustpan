## 1. Failing tests first

- [ ] 1.1 Write failing claimer tests: purpose match, kind match, purpose change during the claim. Check: `go test ./pkg/runner/ -run TestSweepClaimer -count=1` fails on main
- [ ] 1.2 Write failing route tests: report auth, bounds, cut stream, second upload, scope outside the target, finish cases. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` fails on main
- [ ] 1.3 Write failing pool service tests for the purpose refusal. Check: `go test ./internal/runnersvc/ -run TestSweepPurposeInstallationOnly -count=1` fails on main

## 2. Build

- [ ] 2.1 Implement the sweep claimer and wire the poll by pool purpose. Check: `go test ./pkg/runner/ ./internal/api/runnerapi/ -count=1` passes
- [ ] 2.2 Add the report and finish routes with the streaming bound and the encrypted blob write. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` passes
- [ ] 2.3 Refuse a namespace pool with the sweep purpose in the pool service. Check: `go test ./internal/runnersvc/ -run TestSweepPurposeInstallationOnly -count=1` passes
- [ ] 2.4 Document the sweep entries in docs/reference/runner-api.md. Check: `make docs-lint` passes

## 3. Definition of done

- [ ] 3.1 Add an e2e step with a real runner for the sweep claim, report and finish. Check: `make e2e` runs the runner-sweep-claim step against the TLS server
- [ ] 3.2 Check the route actions, the wiring, the docs page and the usability walk, then run the full check. Check: `make check` reports zero findings
