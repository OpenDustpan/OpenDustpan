## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Add failing conformance tests for finalize storage and Latest row read on both dialects Check: TestFinalizeStoresEncryptedBit sqlite and postgres fail on main
- [ ] 1.2 Add failing test that Latest with a present bit performs no blob read Check: TestLatestWithPresentBitReadsNoBlob fails on main because Latest streams the blob
- [ ] 1.3 Add failing test for legacy null-bit fallback Check: TestLegacyNullBitStreamsOnce fails on main expectations by asserting the streamed fallback path
- [ ] 1.4 Add failing test for both-keys refusal with pending cleanup Check: TestBothKeysRefusedRemovesPending fails on main by asserting pending cleanup
- [ ] 1.5 Add failing test for concurrent finalizes and finalize order Check: TestConcurrentFinalizeKeepsWinnerBit fails on main without the column
- [ ] 1.6 Add failing test for shred and redaction precedence with present bit Check: TestShreddedLatestAnswersShreddedWithPresentBit fails without precedence check

## 2. change

- [ ] 2.1 Add the client-encrypted column to each dialect baseline in name order Check: `make generate` plus `sqlc diff` clean with new column in each dialect baseline in name order and no new numbered migration file
- [ ] 2.2 Write the streamed bit at finalize Check: `go test ./pkg/workspace/...` passes with finalize writing the bit in the exposing transaction
- [ ] 2.3 Change Latest to read the stored bit without a blob read Check: `go test ./pkg/workspace/...` passes with Latest reading one row and falling back only on null
- [ ] 2.4 Refuse a document with both encrypted_data and version with invalid state Check: `go test ./pkg/workspace/... -run TestBothKeys` passes
- [ ] 2.5 Add guard test that a version cannot finalize without the bit Check: mutation test that removes the constructor rejection fails the conformance suite

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Use the row read in the existing tool-neutral and TFE outputs paths with no new routes Check: outputs read path shows one row read and no blob read in test log using the existing States Latest port
- [ ] 4.2 Run e2e step against the real CLI over the existing outputs route with a service token Check: `GET /api/v2/workspaces/{workspace_id}/current-state-version-outputs` with a service token returns outputs and blob-read counter stays zero on sqlite and postgres
- [ ] 4.3 Pass make check with zero findings Check: `make check` reports zero findings
