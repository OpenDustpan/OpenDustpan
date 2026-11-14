## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the failing shred test in pkg/store/storetest/item_keys_backup.go: after Shred the key blob is gone and an open fails with the shred error, on both stores. Check: TestItemKeyShredDeletesBlob
- [ ] 1.2 Write the failing round-trip test in the NEW file pkg/secret/itemsealer_blobs_test.go with the namespace ID and the v<version> segment. Check: TestItemKeySealerRoundTripThroughBlobs
- [ ] 1.3 Write the failing tests for a missing record, a seal after shred, a key of another namespace and an open without blob in the NEW file pkg/secret/itemsealer_shred_test.go. Check: TestItemKeyShredMissingRecord
- [ ] 1.4 Write the failing test for a second Shred after a refused blob delete in the NEW file pkg/secret/itemsealer_retry_test.go. Check: TestItemKeyShredRetriesBlobDelete

## 2. Implementation

- [ ] 2.1 Write the sealer wiring, the recorder constructor and Shred in pkg/secret/itemsealer.go. Check: go test -race ./pkg/secret/...
- [ ] 2.2 Correct the orphan cleanup comments in pkg/secret/itemsealer.go and pkg/job/recorder.go. Check: rg -n 'orphan cleanup' pkg/secret/itemsealer.go pkg/job/recorder.go shows no false claim

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the sealer into pkg/job/recorder.go, which takes secret.ItemSealer. The e2e step is BLOCKED by a missing route that shreds an item key. Check: go test -race ./pkg/store/storetest/...
- [ ] 4.2 Add the docs page on shred and key blob delete. The embedder completes the path from the page. Check: make docs-lint
- [ ] 4.3 Run the full check. Check: `make check` with zero findings
