## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing claimer tests: purpose match, kind match, purpose change during the claim. Check: `go test ./pkg/runner/ -run TestSweepClaimer -count=1` fails on main
- [ ] 1.2 Write failing route tests: report auth, bounds, malformed lines, header checks, cut stream, second upload, concurrent finish, finish cases. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` fails on main
- [ ] 1.3 Write failing pool service tests for the purpose refusal and the purpose change. Check: `go test ./internal/runnersvc/ -run TestSweepPurposeInstallationOnly -count=1` fails on main

## 2. Build

- [ ] 2.1 Implement the sweep claimer and wire the poll by pool purpose. Check: `go test ./pkg/runner/ ./internal/api/runnerapi/ -count=1` passes
- [ ] 2.2 Add the report and finish routes with the streaming bound and the encrypted blob write. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` passes
- [ ] 2.3 Refuse a namespace pool with the sweep purpose in the pool service. Check: `go test ./internal/runnersvc/ -run TestSweepPurposeInstallationOnly -count=1` passes
- [ ] 2.4 Document the sweep entries in docs/reference/runner-api.md. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step runner-sweep-claim. A runner joins a sweep pool with a join token on `POST /runner/v1/join`. It polls on `POST /runner/v1/poll` with its runner key proof. The launcher opens a session on `POST /runner/v1/session` with the handoff token. It uploads the report on `POST /runner/v1/session/sweep/report` and finishes on `POST /runner/v1/session/sweep/finish`, both with its job-session key proof. Check: `make e2e` runs the runner-sweep-claim step against the TLS server
- [ ] 4.2 Check the route actions, the wiring, the docs page and the usability walk, then run the full check. Check: `make check` reports zero findings
