## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for exchange, renew, source, credentials, step order, log caps and hash, plan caps and the double commit. Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` fails on main
- [ ] 1.2 Write failing hostile tests: runner key on a session path, session key on a runner path, namespace pool with the sweep purpose, takeover during upload, replayed nonce, reused handoff token. Check: `go test ./internal/api/runnerapi/ -run TestSessionHostile -count=1` fails on main

## 2. Behavior and docs

- [ ] 2.1 Wire each session route to one port call after the claim check. Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` passes
- [ ] 2.2 Write docs/reference/runner-api.md entries for each session path, cap and error code. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the session suites on SQLite and PostgreSQL. Check: `go test -race ./internal/api/runnerapi/... ./pkg/runner/...`
- [ ] 4.2 Add the e2e step for POST /runner/v1/session with a session key proof. Check: `make e2e` runs the session step
- [ ] 4.3 Check the usability of the path from the docs page alone. Check: Each error names one command that helps
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings
