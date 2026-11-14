## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing cases for make at claim, unit check, end on job end, expiry, step grace bound, takeover, retire, renew and loss of grants. Check: go test ./pkg/jobtoken/... -count=1
- [ ] 1.2 Write failing store cases for one live token per claim, a race of two claims, a retry of a claim and a rollback. Check: go test ./pkg/store/... -run TestJobToken on SQLite and PostgreSQL

## 2. Build

- [ ] 2.1 Implement the issuer with hash storage, unit-limited checks and the step grace expiry. Check: go test ./pkg/jobtoken/... -count=1

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the issuer into job.Claimer Claim, Renew and Finish. The route /tofu/state/{namespace}/{workspace} (OpenTofu HTTP backend) accepts the credential kind job-token (identity.CredentialJobToken) for its own unit. Check: go test ./internal/servertest/... -run TestJobTokenWiring
- [ ] 4.2 Add an e2e step that claims a phase through /runner/v1 and calls /tofu/state/{namespace}/{workspace} with the credential kind job-token on its unit and on another unit. BLOCKED for the hand-out to a launcher by the later credentials route; the step takes the token from the test server. Check: make e2e
- [ ] 4.3 Update docs/reference/runner-api.md and run the usability check. Check: make docs-lint
- [ ] 4.4 Pass the full check. Check: make check with zero findings
