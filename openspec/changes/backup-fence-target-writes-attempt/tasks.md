## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Add failing test in the NEW file pkg/backup/attempt_scope_test.go: stale holder late delete after survivor finish keeps survivor files. Check: `go test ./pkg/backup -run TestStaleAttemptDeleteKeepsSurvivor -count=1` fails on main
- [x] 1.2 Add failing test in the NEW file pkg/backup/finish_check_test.go: truncated file fails the finish Check with `backup.corrupt`. Check: `go test ./pkg/backup -run TestFinishRefusesTruncatedFile -count=1` fails on main
- [x] 1.3 Add failing test in the NEW file pkg/backup/orphan_cleanup_test.go: live holder deletes the orphaned attempt prefix and re-lists a recreated stale prefix. Check: `go test ./pkg/backup -run TestOrphanPrefixCleanup -count=1` fails on main

## 2. Fix

- [x] 2.1 Edit the baseline of each dialect in place to add the backups attempt column. Name the baseline file of each dialect in the pull request. Find the baseline files under pkg/store and write each path in the task. Check: `go test ./pkg/backup -count=1` passes
- [x] 2.2 In the NEW file pkg/backup/attempt_prefix.go, make target writes attempt-scoped with the NEW unexported attemptPrefix and NEW newAttemptPrefix, fence each put, delete, and manifest write on the claim token, store the fencing token as the attempt, and reuse Check in the finish step. Check: `go test ./pkg/backup -count=1` passes
- [x] 2.3 Update the stepFinish comment in pkg/backup to state which deletes are covered and add a mutation test in the NEW file pkg/backup/fence_mutation_test.go that removes the pre-call fence check. Check: `go test ./pkg/backup -count=1` passes

## 3. Side effects (computed from main)

- [x] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run e2e backup plus check against the real directory through `ANY /api/server/backups/{}/check` with an access token, with a stale holder delete replayed after finish. Check: `GET /api/server/backups/{id}/check` with an access token reports complete after a stale-holder run against the real directory
- [ ] 4.2 Update the several-instances guide for the attempt layout and cleanup. Check: Several-instances guide shows the attempt layout, orphan cleanup, and retention expiry of each prefix
- [ ] 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
