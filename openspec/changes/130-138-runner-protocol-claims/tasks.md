## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing server test in NEW internal/servertest/runner_claims_test.go for poll on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims`. Check: `TestRunnerClaimPollReturnsOneAssignment` fails on main; calls existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims`.
- [ ] 1.2 Add failing server test in NEW internal/servertest/runner_claims_test.go for heartbeat renew on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/claims/heartbeat`. Check: `TestRunnerHeartbeatRenews60s` fails on main; calls existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW heartbeat path.
- [ ] 1.3 Add failing server test in NEW internal/servertest/runner_claims_test.go for bounded upload on existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW `POST /runner/v1/uploads`. Check: `TestRunnerUploadEnforcesBounds` fails on main; calls existing base route `ANY /runner/v1` with runner key; BLOCKED by NEW upload path.

## 2. Build

- [ ] 2.1 Wire poll, heartbeat, and upload paths in NEW pkg/runner/protocol/claims.go (route declaration and adapter file) on the existing base route `ANY /runner/v1` with runner-key authorization, fence checks, and audit entries. Check: `GET /runner/v1` route test lists the three NEW runner protocol routes with runner-key authorization and fence checks.
- [ ] 2.2 Build the NEW runner client poll and heartbeat loop in NEW pkg/runner/client/claims.go for the runner binary. Check: NEW runner client loop polls, heartbeats every 15 s, and stops on lost lease.
- [ ] 2.3 Write the runner claims docs page in NEW docs/runner-claims.md with poll, heartbeat, and upload examples. Check: Docs page commands run as written against a dev server. Confirmed by: CI (an e2e step in the NEW file e2e/130_138_runner_protocol_claims_docs_test.go runs each command of the docs page against the e2e test server).

- [ ] 2.4 SECURITY. Build the unexported type `claimLease` and its constructor `newClaimLease` (NEW names) in NEW pkg/runner/protocol/claimlease.go. The constructor rejects an empty holder, a stale fence and a non-positive expiry. Check: `go build ./pkg/runner/protocol` passes and no handler holds a separate expiry field.
- [ ] 2.5 SECURITY. Add a hostile test in NEW pkg/runner/protocol/claimlease_test.go named `TestNewClaimLeaseRejectsHostileInput` that calls `newClaimLease` directly with an empty holder, a stale fence and a non-positive expiry. Check: the test passes and each call returns an error.
- [ ] 2.6 SECURITY. Add a hostile test in internal/servertest/runner_claims_test.go named `TestRunnerPollRejectsRetiredAndWrongPoolKey` that polls the existing base route `ANY /runner/v1` (NEW path `POST /runner/v1/claims`) with a runner key as credential, once with a retired key and once with a key of the wrong pool. Check: the test passes, the retired key gets `runner.retired`, the wrong pool key gets `runner.invalid`, and neither gets work.
- [ ] 2.7 SECURITY. Add a hostile test in internal/servertest/runner_claims_test.go named `TestRunnerClaimWritesFailClosedOnMovedFence` that calls the existing base route `ANY /runner/v1` (NEW paths `POST /runner/v1/claims/heartbeat` and `POST /runner/v1/uploads`) with a runner key as credential, and sends heartbeat, upload and complete with a moved fence. Check: the test passes, each write is denied and the stored claim state stays the same.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add e2e step for claim poll and heartbeat in the NEW file e2e/130_138_runner_protocol_claims_docs_test.go on the NEW route `POST /runner/v1/claims` and the NEW route `POST /runner/v1/claims/heartbeat` with the runner key as credential; BLOCKED by NEW `POST /runner/v1/claims`. Check: E2E step polls existing base route `ANY /runner/v1` with a runner key and heartbeats to completion; BLOCKED by NEW `POST /runner/v1/claims`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Run usability check of the runner operator path. Check: Runner operator completes poll and heartbeat from the docs page alone. A person's step.
- [ ] 4.3 Run full checks, route authorization test, and audit verification. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
