## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Add the failing test `TestLoopPollsAndRenewsAfterAThirdOfTheLease` in NEW pkg/runner/client/loop_test.go against a fake server of the routes of main. Check: the test fails on main because the NEW function `client.Run` does not exist, and `pkg/runner/runner.go:228` (func RenewEvery) has no caller that renews.
- [x] 1.2 Add the failing test `TestLoopMapsProtocolCodes` in pkg/runner/client/loop_test.go: `claim-lost`, `retired` and `bad-proof` stop the work, `rate-limited` waits for `Retry-After`. Check: the test fails on main because the NEW function `client.Run` does not exist, and `pkg/runner/protocol/protocol.go:206` (const ErrorClaimLost) has no runner side reader.

## 2. Build

- [x] 2.1 Build the runner loop in NEW pkg/runner/client/loop.go. It polls, renews after `runner.RenewEvery` of the pool lease, heartbeats every 15 s and stops on a lost claim. Check: `go test ./pkg/runner/client` passes.
- [x] 2.4 SECURITY. Add the constructor `New` with its checks in pkg/runner/client/loop.go. Check: `go build ./pkg/runner/client` passes and no code holds an expiry that the server did not send.
- [x] 2.5 SECURITY. Add the hostile test `TestNewRejectsHostileConfig` in pkg/runner/client/loop_test.go that calls `New` with an empty key, a lease below `runner.MinLease` and a URL with another scheme. Check: the test passes and each call returns an error.
- [x] 2.6 SECURITY. Add the hostile test `TestRunnerOfAnotherPoolGetsNoWork` in NEW internal/servertest/runner_claims_test.go. A runner of pool B polls while work waits in pool A. Check: the runner token of pool B (its runner key) polls `POST /runner/v1/poll` and the answer is 204 and the work stays queued.
- [x] 2.7 SECURITY. Add the hostile test `TestSingleRequestsRefuseRetiredAndLostClaim` in internal/servertest/runner_claims_test.go with a retired key and a lost claim against the existing routes. Check: the runner token (the runner key) polls `POST /runner/v1/poll` and renews `POST /runner/v1/claims/{operation}/renew`; the retired key gets `retired` (403), the renew after expiry gets `claim-lost` (409), and neither gets work.

## 3. Side effects (computed from main)

- [x] 3.1 Regenerate the files that the command changes (`make generate`). Never edit a generated file by hand. Check: `make generate` leaves no diff. Confirmed by: the driver's gates.

## 4. Definition of done

- [x] 4.1 Add the e2e step in NEW e2e/130_138_runner_protocol_claims_test.go: a test runner built on `pkg/runner/client` joins, polls, renews and heartbeats until the work ends. Check: the step passes against the e2e test server with the runner token (the runner key) on `POST /runner/v1/poll`, `POST /runner/v1/heartbeat` and `POST /runner/v1/claims/{operation}/renew`.
- [x] 4.2 Run the usability check of the loop library. Check: a Go program of fewer than 40 lines polls, renews and heartbeats through `client.New` and `Run`. Record the program and its output in the pull request body.
- [ ] 4.3 Run full checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
