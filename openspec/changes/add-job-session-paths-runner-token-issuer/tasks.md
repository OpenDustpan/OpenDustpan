## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing cases for make at claim, unit check, end on job end, expiry, step grace bound, takeover, retire, renew and loss of grants in `pkg/jobtoken/issuer_test.go` (NEW). Check: go test ./pkg/jobtoken/... -count=1
- [ ] 1.2 Write failing store cases for one live token per claim, a race of two claims, a retry of a claim and a rollback in `pkg/store/jobtoken_test.go` (NEW). Check: go test ./pkg/store/... -run TestJobToken on SQLite and PostgreSQL

## 2. Build

- [ ] 2.1 Implement the issuer with hash storage, unit-limited checks and the step grace expiry in `pkg/jobtoken/issuer.go` (NEW). Check: go test ./pkg/jobtoken/... -count=1
- [ ] 2.2 Implement the SQL adapters of jobtoken.Store (pkg/jobtoken/store.go:16) in NEW files `pkg/store/sqlite/jobtokens.go` and `pkg/store/postgres/jobtokens.go`, with the NEW table job_tokens in `pkg/store/sqlite/migrations/0168_baseline.sql` and `pkg/store/postgres/migrations/0168_baseline.sql` (in name order) and its queries in the `queries.sql` file of each dialect. A token row references its namespace, its unit and its job by stable ID only (the namespace ID, the unit ID, the job ID). It stores no namespace or unit name as a reference; a name is a label only. The read of a token joins on the IDs, so a rename never changes which token is found. Run the conformance suite `pkg/jobtoken/jobtokentest` on both stores, with a hostile case: a token of a renamed namespace still resolves to the same unit, and a token whose namespace ID is another namespace is not found. Check: `go test -timeout 60m -race ./pkg/store/sqlite/...` passes, and `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race -timeout 60m ./pkg/store/postgres/...` passes

- [ ] 2.3 SECURITY. Build the route restriction for identity.CredentialJobToken in the HTTP handler. A job token is accepted only on the state routes /tofu/state/{namespace}/{workspace} of its own unit. It is refused with 403 on every REST route. Put the restriction in the NEW file `internal/api/tofustate/jobtoken_guard.go` (NEW). The route declaration stays in the route table of its adapter package under internal/api. Check: go test ./internal/api/... -count=1
- [ ] 2.4 SECURITY. Write the hostile test TestJobTokenRefusedOnRESTRoute in the NEW file `internal/api/tofustate/jobtoken_guard_test.go` (NEW). It sends a job token straight to a REST route and expects 403 with no read and no change. Check: go test ./internal/api/... -run TestJobTokenRefusedOnRESTRoute -count=1

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the issuer into job.Claimer Claim, Renew and Finish. BLOCKED by the credentials session route of a later part for the end-to-end hand-out of the token. Until then, the server test claims a phase through the existing runner protocol under /runner/v1 and calls the existing route /tofu/state/{namespace}/{workspace} with the token (credential kind job-token, identity.CredentialJobToken), taking the token from the test server. The task adds the NEW top-level test `TestJobTokenWiring` in a `_test.go` file of `internal/servertest`, so that the Check runs it. Check: go test ./internal/servertest/... -run TestJobTokenWiring
- [ ] 4.2 BLOCKED by the credentials session route of a later part: add an e2e step that claims a phase through /runner/v1 and calls the existing route /tofu/state/{namespace}/{workspace} with the job token (credential kind job-token, identity.CredentialJobToken) on its unit and on another unit; this step is also the e2e step of job-token-authenticator-chain (the refusal on another unit). Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Update docs/reference/runner-api.md and run the usability check. Check: make docs-lint. A person's step.
- [ ] 4.4 Pass the full check. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
