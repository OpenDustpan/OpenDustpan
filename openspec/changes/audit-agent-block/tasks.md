## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the test that a stored chain from before the change passes the chain check. Check: TestChainCheckPassesOldChain
- [ ] 1.2 Write the tests for an entry with the block, one without, two entries that differ in one agent field, and the fixed field order. Check: TestComputeHashAgentBlock
- [ ] 1.3 Write the store conformance test that the six columns round-trip in both dialects. Check: go test -timeout 60m ./pkg/store/...
- [ ] 1.4 Write the API test for the agent field and the job conversion test, including the empty tool, argsDigest and secretStage of a job entry. Check: TestToAuditEntryAgentBlock and TestStartWritesAgentBlock

## 2. Build

- [ ] 2.1 Add the fields, the unexported block type and ComputeHash encoding. Check: TestComputeHashAgentBlock
- [ ] 2.2 Edit the baseline of each dialect in place, in name order, and the queries. Check: sqlc diff is clean
- [ ] 2.3 Add the API field and replace Actor.Agent with job.AgentRef. Check: TestStartWritesAgentBlock

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the block into the server. The route GET /api/audit/entries keeps its audit read action and uses an access token. Check: TestAuditEntriesRouteKeepsItsAction
- [ ] 4.2 Add an e2e step that starts a job with the real CLI and reads the entry. It calls the existing route `GET /api/audit/entries` with an access token, and passes the audit read action of the namespace. Check: make e2e
- [ ] 4.3 Update the audit docs page and check that each error names its command. Check: make docs-lint
- [ ] 4.4 Run the full check. Check: make check with zero findings
