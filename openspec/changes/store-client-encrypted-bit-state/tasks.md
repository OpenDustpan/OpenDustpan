## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [x] 1.1 Record each difference between this spec and main in the implementer's notes (the pull request body), with repository paths such as pkg/workspace/... Check: The pull request body lists each difference
- [x] 1.2 Add failing conformance tests for finalize storage on both dialects in pkg/outputshare/outputsharetest/states.go. Check: TestFinalizeStoresEncryptedBit fails on main
- [x] 1.3 Add a failing test that Latest with a present bit reads no blob in NEW file pkg/workspace/latest_bit_test.go. Check: TestLatestWithPresentBitReadsNoBlob fails on main
- [x] 1.4 Add a failing test for the legacy null-bit fallback in NEW file pkg/workspace/latest_bit_test.go. Check: TestLegacyNullBitStreamsOnce fails on main
- [x] 1.5 Add failing tests for both-keys refusal, concurrent finalize and shred precedence in NEW file pkg/workspace/finalize_bit_test.go. Check: TestBothKeysRefusedRemovesPending, TestConcurrentFinalizeKeepsWinnerBit and TestShreddedLatestAnswersShreddedWithPresentBit fail on main

## 2. change

- [x] 2.1 Add the column to each dialect baseline in name order. Edit the baseline file of each dialect under pkg/store (NEW column in the state version table). Add the same column to the backup column list of the table in pkg/backup/portable_tables.go, because a backup holds every column. Check: `make generate` and `sqlc diff` are clean with no numbered migration file
- [x] 2.2 Write the bit at finalize and read it in Latest in pkg/workspace/state_meta.go. Check: `go test ./pkg/workspace/...` passes
- [ ] 2.3 Add a guard test that removes the constructor rejection in pkg/outputshare/outputsharetest/states.go. Check: The mutation fails the conformance suite

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Confirm the existing outputs routes use the Latest port with no new route Check: Test log shows one row read and no blob read
- [ ] 4.2 Pass the full check Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
