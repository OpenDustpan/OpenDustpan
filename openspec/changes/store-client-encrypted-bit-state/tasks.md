## 1. failing tests

- [ ] 1.1 Add failing conformance tests for finalize storage and Latest row read on both dialects Check: workspace conformance test asserts finalize stores the bit and Latest returns it for plaintext and client-encrypted states on sqlite and postgres
- [ ] 1.2 Add failing test that Latest with a present bit performs no blob read Check: blob-read counter test fails on main because Latest streams the blob
- [ ] 1.3 Add failing test for legacy null-bit fallback Check: conformance test with a null-bit row fails on main expectations by asserting the streamed fallback path

## 2. change

- [ ] 2.1 Add the client-encrypted column to each dialect baseline in name order Check: `make generate` plus `sqlc diff` clean with new column in each dialect baseline in name order and no new numbered migration file
- [ ] 2.2 Write the streamed bit at finalize Check: `go test ./pkg/workspace/...` passes with finalize writing the bit in the exposing transaction
- [ ] 2.3 Change Latest to read the stored bit without a blob read Check: `go test ./pkg/workspace/...` passes with Latest reading one row and falling back only on null
- [ ] 2.4 Refuse a document with both encrypted_data and version with invalid state Check: `go test ./pkg/workspace/... -run TestBothKeys` passes
- [ ] 2.5 Add guard test that a version cannot finalize without the bit Check: mutation test that removes the constructor rejection fails the conformance suite

## 3. definition of done

- [ ] 3.1 Use the row read in the existing tool-neutral and TFE outputs paths with no new routes Check: outputs read path shows one row read and no blob read in test log using the existing States Latest port
- [ ] 3.2 Run e2e step against the real CLI Check: `make e2e` OpenTofu workspace outputs step passes on sqlite and postgres
- [ ] 3.3 Pass make check with zero findings Check: `make check` reports zero findings
