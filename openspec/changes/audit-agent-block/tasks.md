## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the test that a stored chain from before the change passes the chain check, in the conformance suite `pkg/audit/audittest/chain.go`. Check: TestChainCheckPassesOldChain
- [ ] 1.2 Write the tests for an entry with the block, one without, two entries that differ in one agent field, and the fixed field order, in the NEW file `pkg/audit/agent_block_test.go` against `pkg/audit/audit.go:408` (func ComputeHash). Check: TestComputeHashAgentBlock
- [ ] 1.3 Write the store conformance test that the six columns round-trip in both dialects, in `pkg/audit/audittest/entries.go`, run by `pkg/store/sqlite/audit_entries.go` and `pkg/store/postgres`. Check: go test -timeout 60m ./pkg/store/...
- [ ] 1.4 Write the API test for the agent field in `internal/api/audit/audit.go` (the entry JSON at line 135) and the job conversion test in `pkg/job/service_start.go:623` (the agent change key), including the empty tool, argsDigest and secretStage of a job entry. Check: TestToAuditEntryAgentBlock and TestStartWritesAgentBlock

## 2. Build

- [ ] 2.1 Add the six fields to `pkg/audit/audit.go:232` (type Entry), the unexported block type, and the encoding in `pkg/audit/audit.go:408` (func ComputeHash). Check: TestComputeHashAgentBlock
- [ ] 2.2 Edit the baseline of each dialect in place, in name order: `pkg/store/sqlite/migrations/0168_baseline.sql:237` and `pkg/store/postgres/migrations/0168_baseline.sql:77` (the table audit_entries), and the queries in `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql`; write the columns in `pkg/store/sqlite/audit.go:27` (func Store.Append) and its PostgreSQL twin. Check: sqlc diff is clean
- [ ] 2.3 Add the API field in `internal/api/audit/audit.go` (the entry JSON at line 135), and replace `Actor.Agent` (`pkg/job/job.go:383`) with the NEW type `job.AgentRef` in `pkg/job/job.go`; `pkg/job/service_start.go:623` writes the block in place of the change key. Check: TestStartWritesAgentBlock

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the block into the server. The route GET /api/audit/entries (declared and served in `internal/api/audit/audit.go`) keeps its audit read action and uses an access token. Check: TestAuditEntriesRouteKeepsItsAction
- [ ] 4.2 Add an e2e step in the NEW file `e2e/audit_agent_block_test.go` that starts a job with the real CLI and reads the entry. It calls the existing route `GET /api/audit/entries` with an access token, and passes the audit read action of the namespace. The agent writes the test; CI runs it. Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Update the audit docs page `docs/guides/audit-log.md` and check that each error names its command. Check: make docs-lint
- [ ] 4.4 Run the full check. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
