## 1. Failing tests first

- [ ] 1.1 Write failing tests for exchange, renew handoff, source, credentials, step order, log caps and hash, plan caps and the double commit. Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` fails on main
- [ ] 1.2 Write failing hostile tests: runner key on a session path, session key on a runner path, sweep-purpose pool, takeover during upload, failed snapshot, absent LFS object. Check: `go test ./internal/api/runnerapi/ -run TestSessionHostile -count=1` fails on main

## 2. Behavior and docs

- [ ] 2.1 Wire each session route to one port call after the session claim check, with the protocol error codes. Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` passes
- [ ] 2.2 Write docs/reference/runner-api.md entries for each session path, cap and error code. Check: `make docs-lint` passes

## 3. Definition of done

- [ ] 3.1 Run the session suites on SQLite and PostgreSQL. Check: `go test -race ./internal/api/runnerapi/... ./pkg/runner/...`
- [ ] 3.2 Add an e2e step with the real launcher against the real server for the session paths, with a route test that lists the action runner:claim for each path. Check: `make e2e` runs the session step
- [ ] 3.3 Check the usability of the path from the docs page alone. Check: Each error names one command that helps
- [ ] 3.4 Run the full check. Check: `make check` reports zero findings
