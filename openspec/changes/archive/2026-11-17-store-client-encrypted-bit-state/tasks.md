## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [x] 1.1 Record each difference between this spec and main in the implementer's notes (the pull request body), with repository paths such as pkg/workspace/... Check: The pull request body lists each difference
- [x] 1.2 Conformance test for finalize storage on each dialect: `testFinalizeStoresClientEncryptedBit` in pkg/store/storetest/workspaces.go. Check: the test passes on each dialect, and `testConcurrentFinalize` in the same file checks that the exposed version holds the bit of the winner
- [x] 1.3 Test that Latest with a present bit reads no blob: `TestLatestAnswersTheBitWithoutABlobRead` in pkg/workspace/state_encrypted_bit_test.go. Check: the test passes
- [x] 1.4 Test of the legacy null-bit fallback: `TestLatestStreamsTheBlobWhenTheBitIsNull` in pkg/workspace/state_encrypted_bit_test.go. Check: the test passes
- [x] 1.5 Tests of the both-keys refusal: `TestBothKeys`, `TestBothKeysFoldedVersion` and `TestStreamedWriteRefusesBothKeys` in pkg/workspace/state_encrypted_bit_test.go. Check: the tests pass, and each refusal leaves no version and no state blob. The test for shred precedence is not written: see the pull request

## 2. change

- [x] 2.1 Add the column to each dialect baseline in name order. Edit the baseline file of each dialect under pkg/store (NEW column in the state version table). Add the same column to the backup column list of the table in pkg/backup/portable_tables.go, because a backup holds every column. Check: `make generate` and `sqlc diff` are clean with no numbered migration file
- [x] 2.2 Write the bit at finalize and read it in Latest in pkg/workspace/state_meta.go. Check: `go test ./pkg/workspace/...` passes
- [x] 2.3 Guard test of the rejection of an unknown bit at finalize: `testFinalizeWithoutTheBitIsRefused` in the store conformance suite pkg/store/storetest/workspaces.go, which runs on each dialect. Check: removing the rejection in `FinalizeStateVersionRow` (pkg/store/internal/codec/workspaces.go) fails `TestWorkspaceConformance` with "want ErrStateBitUnknown"

## 3. Side effects (computed from main)

- [x] 3.1 Regenerate `internal/api/apidesc/openapi.json` (run `TestAPIDescriptionGolden` with its `-update-api-description` flag) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the change adds no API field, so both generators give no diff

## 4. definition of done

- [x] 4.1 Confirm the existing outputs routes use the Latest port with no new route Check: `TestLatestAnswersTheBitWithoutABlobRead` and `TestLatestStreamsTheBlobWhenTheBitIsNull` pass in pkg/workspace; the first counts no blob read with a present bit
- [x] 4.2 Pass the full check Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
