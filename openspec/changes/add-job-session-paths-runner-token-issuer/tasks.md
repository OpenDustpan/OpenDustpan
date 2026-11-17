## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing cases for the expiry rule (claim end earlier, step end plus grace earlier, both equal, a past time) in `pkg/jobtoken/expiry_test.go` (NEW) as the NEW top-level test TestExpiryRule. Check: go test ./pkg/jobtoken/... -run TestExpiryRule -count=1 fails on main because the NEW symbol `jobtoken.Expiry` does not exist and `pkg/jobtoken/service.go:63` has no expiry rule (NEW function Expiry)
- [ ] 1.2 Write failing tests in `internal/api/runnerapi/session_credentials_test.go` (NEW): the credentials request of a live session answers one token with the earlier expiry and writes one audit entry that names the job, the step and the expiry; the claim answer holds no job token (TestClaimAnswerHoldsNoJobToken); a request after the claim ended, after takeover and after retire is refused, makes no token and writes one denial audit entry that names the job, the step and the reason; the same for a job with no stable unit ID, a lost grant and a sweep request; the denial entry holds no token text. The tests are the NEW top-level test TestSessionCredentials. Check: go test ./internal/api/runnerapi/ -run TestSessionCredentials -count=1 fails on main because main routes no credentials path (`internal/api/runnerapi/routes.go:92-199` has no `protocol.PathSessionCredentials`)

## 2. Build

- [x] 2.1 Implement the expiry rule in `pkg/jobtoken/expiry.go` (NEW) as a pure function of the claim end, the step start, the step timeout and the grace. Check: go test ./pkg/jobtoken/... -run TestExpiryRule -count=1
- [ ] 2.2 Not applicable: implemented by #2916 (SQL job token stores for SQLite and PostgreSQL, with the conformance suite and the hostile cases) and by `jobtoken.Service.Issue` on main (`pkg/jobtoken/service.go:63`).
- [ ] 2.3 SECURITY. Add the credentials path of the job session in `internal/api/runnerapi/session_credentials.go` (NEW): after the claim check, load the job and its live claim, build the request with an audit entry that names the job, the step and the expiry, call `jobtoken.Service.Issue`, and answer the plaintext once in `protocol.CredentialsResponse`. Declare the route in `internal/api/runnerapi/routes.go` with its action. The answer for a sweep holds no token. Each refusal writes one denial audit entry that names the job, the step and a reason code, and holds no token text, with a hostile test for the boundary check, the claim check and the grant check, each run as if the other layers failed. Check: go test ./internal/api/runnerapi/... -count=1
- [ ] 2.4 SECURITY. Write the hostile test TestJobTokenRefusedOnRESTRoute in `pkg/dustpan/jobtoken_rest_refusal_test.go` (NEW). It takes a token from the new credentials path and sends it straight to a REST route and to a unit of another job, and expects 403 with no read and no change. Check: go test ./pkg/dustpan/... -run TestJobTokenRefusedOnRESTRoute -count=1

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [x] 4.1 Add the NEW top-level test `TestJobTokenWiring` in a `_test.go` file of `internal/servertest`: claim a phase through /runner/v1, open the job session, ask for the credentials of a step, and call the route /tofu/state/{namespace}/{workspace} with the token (credential kind job-token, identity.CredentialJobToken). Check: go test ./internal/servertest/... -run TestJobTokenWiring
- [ ] 4.2 Add an e2e step in `e2e/job_token_credentials_test.go` (NEW) that does the path of 4.1 with the real launcher: it opens the job session with the handoff token on the session open path, then calls the NEW route `POST /runner/v1/session/credentials` with the session key proof (action runner:claim), on the unit of the job and on another unit; this step is also the e2e step of the job-token authenticator chain (the refusal on another unit). Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [x] 4.3 Update docs/reference/runner-api.md with the credentials path and run the usability check. Check: make docs-lint. A person's step.
- [ ] 4.4 Pass the full check. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
