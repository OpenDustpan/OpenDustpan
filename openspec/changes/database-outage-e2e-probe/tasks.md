## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing probe tests first

- [ ] 1.1 Add failing long outage probe test in the NEW file `e2e/ha/database_outage_test.go` that calls existing route `GET /health/ready` on each instance and existing route `ANY /api/v2/state-versions` through the proxy with credential kind access token, with 60 s stop, readiness and 503 assertions, retry timing, cancel, recovery, and no-change preview and plan Check: `go test ./e2e/ha -run TestDatabaseOutageLongOutage -count=1` fails on main with missing probe. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add failing short outage probe test in the NEW file `e2e/ha/database_outage_test.go` that calls existing route `GET /health/ready` and existing route `ANY /api/v2/state-versions` through the proxy with credential kind access token, with short stop at the provisional margin below the smallest retry window and exit 0 assertions Check: `go test ./e2e/ha -run TestDatabaseOutageShortOutage -count=1` fails on main with missing probe. Confirmed by: CI (the agent writes the e2e test).

## 2. Probe work

- [ ] 2.1 BLOCKED by the missing database proxy control route and its credential kind: main has no such route. Add the NEW database proxy control in the NEW file `e2e/ha/database_outage_probe.go` with deferred restore and stop check. The proxy control is a test fixture and needs no credential. Poll readiness only through the `GET /health/ready` route. That route needs no credential. Check: NEW suite fixture `NewDatabaseOutageProbe` refuses an empty CLI matrix in a unit test
- [ ] 2.2 Record in the NEW file `e2e/ha/database_outage_retry.go` per CLI version how long one write retries before the command fails in the long outage and feed the short window in the same run, using existing route `ANY /api/v2/state-versions` with credential kind access token and the CLI matrix from the suite config Check: `go test ./e2e/ha -run TestDatabaseOutageRetryReport -count=1` shows per-version retry seconds from existing route `ANY /api/v2/state-versions` writes with credential kind access token

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the probe to the real proxy, the real instances, and the real CLI binaries with credential kind access token, skipped with the missing part named when a depended part is not merged Check: Probe calls existing route `GET /health/ready` on each instance and the existing TFE-compatible state version and lock routes through the proxy with credential kind access token
- [ ] 4.2 Write the NEW docs page `docs/guides/database-outage-probe.md` for the long and short outage steps and the retry report Check: Docs page for the outage probe runs each command as written
- [ ] 4.3 Run usability check from the docs page alone and full checks Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
