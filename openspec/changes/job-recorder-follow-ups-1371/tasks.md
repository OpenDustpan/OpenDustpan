## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add TestLogReaderCloseIsSticky in pkg/job/artifactreader_test.go (NEW file). Check: `go test ./pkg/job/ -run TestLogReaderCloseIsSticky -count=1` fails on main
- [ ] 1.2 Add TestCheckChunkRowRefusesBadRows in pkg/store/internal/jobstore/record_test.go (NEW file). Check: `go test ./pkg/store/internal/jobstore/ -run TestCheckChunkRowRefusesBadRows -count=1` fails on main

## 2. Fix

- [ ] 2.1 Make the hash error sticky in pkg/job/artifactreader.go:294 (func Read). Check: `go test ./pkg/job/ -count=1`
- [ ] 2.2 Check the sha256 format and the job and namespace agreement in pkg/store/internal/jobstore/record.go:772 (func CheckChunkRow) by extending the shared helper from follow-ups-1431-review-one after it lands. Check: `go test ./pkg/store/internal/jobstore/ -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check the restore path through ANY /api/server/backups/check with an access token. Check: `go test ./pkg/backup/ -run TestCheckRestoreMissingBlob -count=1` through ANY /api/server/backups/check with an access token
- [ ] 4.2 Run the docs and usability check for the restore error. A person's step. Check: `make check` with zero findings
