## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test in the NEW file pkg/backup/attempt_scope_test.go: stale holder late delete after survivor finish keeps survivor files. Check: `go test ./pkg/backup -run TestStaleAttemptDeleteKeepsSurvivor -count=1` fails on main
- [ ] 1.2 Add failing test in the NEW file pkg/backup/finish_check_test.go: truncated file fails the finish Check with `backup.corrupt`. Check: `go test ./pkg/backup -run TestFinishRefusesTruncatedFile -count=1` fails on main
- [ ] 1.3 Add failing test in the NEW file pkg/backup/orphan_cleanup_test.go: live holder deletes the orphaned attempt prefix and re-lists a recreated stale prefix. Check: `go test ./pkg/backup -run TestOrphanPrefixCleanup -count=1` fails on main

## 2. Fix

- [ ] 2.1 Edit the baseline of each dialect in place to add the backups attempt column (the table backups is a control table that a backup leaves out, `TableBackups` at pkg/backup/backup.go:81, so pkg/backup/portable_tables.go does not list it), in its place in the name order: pkg/store/sqlite/migrations/0168_baseline.sql and pkg/store/postgres/migrations/0168_baseline.sql. The baselines have no `.sha256` file until the first tag (ADR 362 decision 4). The task adds the NEW top-level test `TestFrozenMigrations` in a `_test.go` file of `pkg/backup`, so that the Check runs it. Check: `go test ./pkg/backup -count=1` passes and `go test -timeout 60m ./pkg/store/sqlite -run TestFrozenMigrations -count=1` passes
- [ ] 2.2 In the NEW file pkg/backup/attempt_prefix.go, make target writes attempt-scoped with the NEW unexported attemptPrefix and NEW newAttemptPrefix, fence each put, delete, and manifest write on the claim token, store the fencing token as the attempt, and reuse Check in the finish step. Check: `go test ./pkg/backup -count=1` passes
- [ ] 2.3 Update the stepFinish comment in pkg/backup to state which deletes are covered and add a mutation test in the NEW file pkg/backup/fence_mutation_test.go that removes the pre-call fence check. Check: `go test ./pkg/backup -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Set Attempt in each existing literal of backup.Record: the attempt column of task 2.1 is the NEW field `Attempt` of `backup.Record` (pkg/backup/backup.go:263), and exhaustruct asks for it in each non-empty literal on main: `internal/api/server/routes_test.go`, `pkg/backup/service.go`, `pkg/dustpan/dustpan_test.go`, `pkg/store/internal/codec/codec.go`, `pkg/store/postgres/backup.go`, `pkg/store/storetest/backup.go`, `pkg/store/storetest/backup_test.go`, `pkg/store/storetest/control_minimum.go`, `pkg/store/storetest/shared_running.go`. No lint exception and no linter suppression comment. Check: `golangci-lint run ./...` reports no exhaustruct finding.
- [x] 3.2 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Run e2e backup plus check against the real directory through `ANY /api/server/backups/{}/check` with an access token, with a stale holder delete replayed after finish. Check: `GET /api/server/backups/{id}/check` with an access token reports complete after a stale-holder run against the real directory
- [ ] 4.2 Update the existing file docs/guides/several-instances.md for the attempt layout and the cleanup. Check: docs/guides/several-instances.md shows the attempt layout and the orphan cleanup by the live holder. `make docs-lint` passes on the edited guide (sentence cap 25 words).
- [ ] 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
