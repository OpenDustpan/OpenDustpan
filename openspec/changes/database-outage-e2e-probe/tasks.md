## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing probe tests first

- [x] 1.1 Add failing long outage probe test in the NEW file `e2e/ha/database_outage_test.go` that calls existing route `GET /health/ready` on each instance and existing route `ANY /api/v2/state-versions` through the proxy with credential kind access token, with 60 s stop, readiness and 503 assertions, retry timing, cancel, recovery, and no-change preview and plan Check: NEW TestDatabaseOutageLongOutage in e2e/ha/database_outage_test.go (NEW): `go test -tags e2e ./e2e/ha -run TestDatabaseOutageLongOutage -count=1` compiles the test; the claim that it fails on main (the file and the test do not exist) is confirmed by CI. Confirmed by: CI (the e2e job with several instances runs the outage probe; the local run skips, and the local run proves only that the test compiles).
- [ ] 1.2 Add failing short outage probe test in the NEW file `e2e/ha/database_outage_test.go` that calls existing route `GET /health/ready` and existing route `ANY /api/v2/state-versions` through the proxy with credential kind access token, with short stop at the provisional margin below the smallest retry window above 0 s, exit 0 assertions for each write kind with a window above 0 s, and clean-failure assertions (non-zero exit, no corrupt state, no lock, next run exit 0 with exactly one new state version) for each kind with a window of 0 s Check: NEW TestDatabaseOutageShortOutage in e2e/ha/database_outage_test.go (NEW): `go test -tags e2e ./e2e/ha -run TestDatabaseOutageShortOutage -count=1` compiles the test; the claim that it fails on main (the file and the test do not exist) is confirmed by CI. Confirmed by: CI (the e2e job with several instances runs the outage probe; the local run skips, and the local run proves only that the test compiles).

## 2. Probe work

- [x] 2.1 BLOCKED by the missing database proxy control route and its credential kind: main has no such route. Add the NEW database proxy control in the NEW file `e2e/ha/database_outage_probe.go` with deferred restore and stop check. The proxy control is a test fixture and needs no credential. Poll readiness only through the `GET /health/ready` route. That route needs no credential. Check: NEW suite fixture `NewDatabaseOutageProbe` refuses an empty CLI matrix in a unit test
- [x] 2.2 Record in the NEW file `e2e/ha/database_outage_retry.go` per CLI version and per write kind (checkpoint, events, complete, OpenTofu state version; 0 s for a kind that is not retried) how long one write retries before the command fails in the long outage and feed the short window in the same run, using existing route `ANY /api/v2/state-versions` with credential kind access token and the CLI matrix from the suite config Check: `go test -tags e2e ./e2e/ha -run TestDatabaseOutageRetryReport -count=1` shows per-version and per-kind retry seconds from existing route `ANY /api/v2/state-versions` writes with credential kind access token Confirmed by: CI (the e2e job with several instances runs the outage probe; the local run skips, and the local run proves only that the test compiles).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the probe to the real proxy, the real instances, and the real CLI binaries with credential kind access token, skipped with the missing part named when a depended part is not merged Check: Probe calls existing route `GET /health/ready` on each instance and the existing TFE-compatible state version and lock routes through the proxy with credential kind access token
- [x] 4.2 Write the NEW docs page `docs/guides/database-outage-probe.md` for the long and short outage steps and the retry report Check: Docs page for the outage probe runs each command as written. `make docs-lint` passes on each edited guide.
- [ ] 4.3 Run usability check from the docs page alone and full checks Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only)
