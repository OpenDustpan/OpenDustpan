## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing audit append race test through AppendChain with two claims on one head. Check: `go test ./pkg/audit/... -run TestChainConcurrent -count=1` fails on main and passes after
- [ ] 1.2 Add failing shared clock test for IsExpired on lease, session, and sign-in expiry with a skewed process clock. Check: `go test ./pkg/store/... -run TestDatabaseClock -count=1` fails on main and passes after

## 2. Change

- [ ] 2.1 Change audit append to conditional UPDATE on the expected head with the fence check and change expiry reads to the database clock. Check: `go test ./pkg/store/storetest/... -count=1` with `DUSTPAN_TEST_POSTGRES_URL` set passes on both dialects
- [ ] 2.2 Wire the mismatch error with the new head and the 503 clock gap in the API error shape. Check: `go test ./pkg/audit/... -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e alternation step against the real CLI through the round-robin proxy. Check: `go test ./e2e -run TestAlternationAudit -count=1` calls `ANY /api/audit/entries`, passes the namespace grant check, uses an access token, and shows appends from each instance through the round-robin proxy
- [ ] 4.2 Measure audit append latency on the two-instance setup. Check: Run `go test ./internal/servertest -run TestAuditAppendLatency -count=1` against `ANY /api/audit/entries` with an access token; p99 of the audit append transaction stays below the quality target on PostgreSQL
- [ ] 4.3 Update docs/guides/several-instances.md expiry and chain notes in the same pull request. Check: Guide page commands run as written
- [ ] 4.4 Run usability check of the operator path. Check: Operator reads the path alone and each error names the one command that helps
- [ ] 4.5 Run definition of done: wiring, routes ANY /api/audit/entries and ANY /api/server/session with authorization actions, e2e step, docs page, usability check, `make check`. Check: `make check` reports zero findings with PostgreSQL cases run against `DUSTPAN_TEST_POSTGRES_URL`
