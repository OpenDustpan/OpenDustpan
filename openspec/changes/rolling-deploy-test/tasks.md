## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing e2e step for rolling deploy with mixed version pool that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `go test ./e2e -run TestHARollingDeploy -v` fails on main with missing step
- [ ] 1.2 Add failing check that old binary writes get schema.binary-below-minimum during fake migration that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `go test ./e2e -run TestHARollingDeployMixedVersionRefusal -v` fails on main with missing check

## 2. Test step

- [ ] 2.1 Run three serve binaries behind round-robin TLS proxy with tofu apply and pulumi up loops and zero failed commands that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `make e2e-ha ROLLING_DEPLOY=1` shows proxy record with each of three instances serving each tool and zero failed CLI exits
- [ ] 2.2 Replace binaries one at a time with fake contract migration and keep loops green that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `make mixed-version-test` passes with test-only minimum bump, and passes with last 2 tags when a v tag exists

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire step into high availability suite with proxy record and operation log that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `make e2e-ha` passes rolling deploy step against real CLI binaries
- [ ] 4.2 Use existing instances route for diagnosis. Check: `GET /api/server/instances` lists three instances during step with access token
- [ ] 4.3 Run usability check of rolling replace path. Check: Operator completes replace from existing high availability guide alone with clear version error
- [ ] 4.4 Run full checks. Check: `make check` with zero findings
