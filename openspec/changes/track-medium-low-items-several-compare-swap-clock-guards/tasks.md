## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing audit append race test in pkg/audit/audittest/chain.go:615 (func testChainConcurrentEnds) through AppendChain (NEW method on the audit store port) with two claims on one head. The task adds the NEW top-level test `TestChainConcurrent` in a `_test.go` file of `pkg/audit`, so that the Check runs it. Check: `go test ./pkg/audit/... -run TestChainConcurrent -count=1` fails on main and passes after
- [ ] 1.2 Add failing shared clock test in pkg/store/storetest/attempt_deadline.go:286 (func testDatabaseClock) for IsExpired (NEW func) on lease, session, and sign-in expiry with a skewed process clock. The task adds the NEW top-level test `TestDatabaseClock` in a `_test.go` file of `pkg/store`, so that the Check runs it. Check: `go test ./pkg/store/... -run TestDatabaseClock -count=1` fails on main and passes after

- [ ] 1.3 SECURITY. Add hostile test TestChainAppendWithoutGrant in pkg/audit/audittest/chain.go. It calls AppendChain without the grant on the namespace. Check: `go test ./pkg/audit/... -run TestChainAppendWithoutGrant -count=1` shows a refusal and no head move
- [ ] 1.4 SECURITY. Add hostile test TestChainAppendEntryTooLarge in pkg/audit/audittest/chain.go. It calls AppendChain with an entry above the size bound. Check: `go test ./pkg/audit/... -run TestChainAppendEntryTooLarge -count=1` shows a refusal before the chain write and no head move

## 2. Change

- [ ] 2.1 Change audit append in pkg/audit/chain.go to conditional UPDATE on the expected head with the fence check and change expiry reads to the database clock. Check: `go test ./pkg/store/storetest/... -count=1` with `DUSTPAN_TEST_POSTGRES_URL` set passes on both dialects
- [ ] 2.2 Wire the mismatch error with the new head and the 503 clock gap in the API error shape in pkg/audit/chain.go. Check: `go test ./pkg/audit/... -count=1` passes

- [ ] 2.3 SECURITY. Add the grant check at point of use to audit append in pkg/audit/chain.go, in the same transaction as the head check. Check: `go test ./pkg/audit/... -run TestChainAppendWithoutGrant -count=1` passes
- [ ] 2.4 SECURITY. Add the entry size bound to audit append in pkg/audit/chain.go, applied before the chain write. Check: `go test ./pkg/audit/... -run TestChainAppendEntryTooLarge -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e alternation step against the real CLI through the round-robin proxy. The task adds the NEW top-level test `TestAlternationAudit` in a `_test.go` file of `e2e`, so that the Check runs it. Check: `go test ./e2e -run TestAlternationAudit -count=1` calls `ANY /api/audit/entries`, passes the namespace grant check, uses an access token, and shows appends from each instance through the round-robin proxy
- [ ] 4.2 Measure audit append latency on the two-instance setup. The task adds the NEW top-level test `TestAuditAppendLatency` in a `_test.go` file of `internal/servertest`, so that the Check runs it. Check: Run `go test ./internal/servertest -run TestAuditAppendLatency -count=1` against `ANY /api/audit/entries` with an access token; p99 of the audit append transaction stays below the quality target on PostgreSQL
- [ ] 4.3 Update docs/guides/several-instances.md expiry and chain notes in the same pull request. Check: Guide page commands run as written. Confirmed by: CI (an e2e step in the NEW file e2e/track_medium_low_items_several_compare_swap_clock_guards_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.4 Run usability check of the operator path. Check: Operator reads the path alone and each error names the one command that helps. A person's step.
- [ ] 4.5 Run definition of done: wiring, routes ANY /api/audit/entries and ANY /api/server/session with authorization actions, e2e step, docs page, usability check, `make check`. Check: `make check` reports zero findings with PostgreSQL cases run against `DUSTPAN_TEST_POSTGRES_URL`. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
