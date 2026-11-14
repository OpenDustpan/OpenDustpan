## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Add failing store test in pkg/store/storetest/restore_worker_cases.go:534 (func workerRig.failoverVersion) for start with staging name and failover version bump on SQLite and PostgreSQL. Check: TestRestoreStartStoresStagingName fails on main, passes after.
- [ ] 1.2 Add failing test in NEW file pkg/backup/restore_fenced_steps_test.go for copy, swap, drop-old, and cleanupStage with stale fencing token refused. Check: TestRestoreFencedStepsRefuseStaleToken fails on main, passes after.
- [ ] 1.3 Add failing test in NEW file pkg/store/postgres/restore_dropold_test.go for drop-old with 5 s lock wait and backoff retry. Check: TestRestoreDropOldRetriesOnLockTimeout fails on main, passes after.
- [ ] 1.4 Add failing tests in internal/servertest/restore_lease_test.go for 409 restore.running and 403 restore.forbidden on start and cancel. The tests call route `ANY /api/server/restores` with an access token. Check: TestRestoreSecondStartAnswers409 and TestRestoreStartWithoutRightAnswers403 fail on main, pass after.

## 2. work

- [ ] 2.1 Change pkg/store/<dialect>/migrations/<NNNN>_baseline.sql of each dialect in place with staging-name column; run `make generate`. Check: `go test -race ./pkg/store/... -run Restore -timeout 60m` passes.
- [ ] 2.2 Wire durable kind restore with fenced steps and non-failing start in pkg/store/postgres/restore_operation.go:100 (func fencedRestore) on `ANY /api/server/restores` with access token. Check: `go test -race ./pkg/backup/...` passes on `ANY /api/server/restores` with access token.
- [ ] 2.3 Prove takeover with 2 instances in `internal/servertest` on `ANY /api/server/restores/{}` with access token. Check: Kill holder during copy and swap in `internal/servertest` on `ANY /api/server/restores/{}` with access token shows taker success.
- [ ] 2.4 Add the NEW error code restore.lock-wait to the restore codes of the catalog in internal/errcode/catalog_restore.go, with its message and the one command that helps, and its row in internal/errcode/catalog_test.go. Check: `go test ./internal/errcode/...`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Wire route ANY /api/server/restores with authorization actions and worker check with access token. Check: `GET ANY /api/server/restores/{}` with access token shows fenced steps and staging name.
- [ ] 4.2 Add e2e step against real directory with kill during restore on `ANY /api/server/restores` with access token. Check: E2e step on `ANY /api/server/restores` with access token kills holder during restore and restore ends succeeded on taker.
- [ ] 4.3 Write docs page for several-instances restore path. Check: Docs page for durable restore in docs/guides/several-instances.md runs each command as written.
- [ ] 4.4 Run usability check and `make check` with zero findings. Check: `make check` reports zero findings.
