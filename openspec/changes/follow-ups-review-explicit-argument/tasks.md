## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write TestNewServerChainFull in `pkg/dustpan/dustpan_test.go` covering NewServer in `pkg/dustpan/dustpan.go`: compare the whole chain and expect the token gate inside identity.Chain from `pkg/dustpan/area_signin.go` Check: go test ./pkg/dustpan -run TestNewServerChainFull fails on main
- [ ] 1.2 Write one lone unit test of the dev guard in `pkg/dustpan/devrefusal_internal_test.go` so a failure names the dev guard Check: Lone dev guard test fails when its guard is removed
- [ ] 1.3 Write TestBackupOnlyWorkerHeartbeat in NEW `pkg/dustpan/workerwatch_test.go`: a worker with only backups reports a heartbeat via `pkg/dustpan/workerwatch.go` Check: go test ./pkg/dustpan -run TestBackupOnlyWorkerHeartbeat fails on main

## 2. Work

- [ ] 2.1 Run rg search and rename the entries field of profileInputs to valueEntries in `pkg/profile/profile.go`: update each use in the file that rg names Check: rg -n profileInputs `pkg/dustpan/dustpan.go` names the type file, old entries field has no match, package tests pass
- [ ] 2.2 Wire the worker watch from `pkg/dustpan/workerwatch.go` into newBackupService in `pkg/dustpan/area_backup.go` Check: TestBackupOnlyWorkerHeartbeat passes

## 3. Definition of done

- [ ] 3.1 Run the touched package tests and make check for the change in `pkg/dustpan/dustpan.go` Confirmed by: CI (make check) Check: make check reports zero findings
