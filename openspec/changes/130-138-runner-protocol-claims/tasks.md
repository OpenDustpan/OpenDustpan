## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing server test in NEW internal/servertest/runner_claims_test.go for poll on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims`. Check: `TestRunnerClaimPollReturnsOneAssignment` fails on main; calls existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims`.
- [ ] 1.2 Add failing server test in NEW internal/servertest/runner_claims_test.go for heartbeat renew on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims/heartbeat`. Check: `TestRunnerHeartbeatRenews60s` fails on main; calls existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW heartbeat path.
- [ ] 1.3 Add failing server test in NEW internal/servertest/runner_claims_test.go for bounded upload on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/uploads`. Check: `TestRunnerUploadEnforcesBounds` fails on main; calls existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW upload path.

## 2. Build

- [ ] 2.1 Wire poll, heartbeat, and upload paths in NEW pkg/runner/protocol/claims.go (route declaration and adapter file) on the existing base route `ANY /runner/v1` with runner-key authorization, fence checks, and audit entries. Check: `GET /runner/v1` route test lists the three NEW runner protocol routes with runner-key authorization and fence checks.
- [ ] 2.2 Build the NEW runner client poll and heartbeat loop in NEW pkg/runner/client/claims.go for the runner binary. Check: NEW runner client loop polls, heartbeats every 15 s, and stops on lost lease.
- [ ] 2.3 Write the runner claims docs page in NEW docs/runner-claims.md with poll, heartbeat, and upload examples. Check: Docs page commands run as written against a dev server.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step for claim poll and heartbeat on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims`. Check: E2E step polls existing base route `ANY /runner/v1` with a runner key and heartbeats to completion; BLOCKED by NEW `POST /runner/v1/claims`.
- [ ] 4.2 Run usability check of the runner operator path. Check: Runner operator completes poll and heartbeat from the docs page alone.
- [ ] 4.3 Run full checks, route authorization test, and audit verification. Check: `make check` reports zero findings.
