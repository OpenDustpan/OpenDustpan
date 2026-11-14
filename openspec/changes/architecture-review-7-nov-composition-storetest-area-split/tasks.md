## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Split

- [ ] 1.1 Record the ordered workspace subtest names on main on SQLite and PostgreSQL into the NEW files /var/tmp/storetest-before-workspace-sqlite.txt and /var/tmp/storetest-before-workspace-postgres.txt. Check: `go test -v ./pkg/store/storetest/ -run RunWorkspace` writes the NEW file /var/tmp/storetest-before-workspace-sqlite.txt
- [ ] 1.2 Record the ordered update subtest names on main on both dialects into the NEW files /var/tmp/storetest-before-updates-sqlite.txt and /var/tmp/storetest-before-updates-postgres.txt. Check: `go test -v ./pkg/store/storetest/ -run RunUpdates` writes the NEW file /var/tmp/storetest-before-updates-sqlite.txt
- [ ] 1.3 Move workspace cases by area into the NEW files pkg/store/storetest/workspaces_lock.go, workspaces_state.go, workspaces_outputs.go, workspaces_history.go, workspaces_inventory.go and workspaces_delete.go, and concatenate in RunWorkspace. Check: `go test ./pkg/store/storetest/` passes with identical subtest names in identical order
- [ ] 1.4 Move update cases by area into the NEW files pkg/store/storetest/updates_<area>.go and concatenate in RunUpdates. Check: `go test ./pkg/store/storetest/` passes with identical subtest names in identical order
- [ ] 1.5 Write the after lists to the NEW files /var/tmp/storetest-after-workspace-sqlite.txt, /var/tmp/storetest-after-workspace-postgres.txt, /var/tmp/storetest-after-updates-sqlite.txt and /var/tmp/storetest-after-updates-postgres.txt from pkg/store/storetest, and diff them against the before lists. Check: `diff /var/tmp/storetest-before-workspace-sqlite.txt /var/tmp/storetest-after-workspace-sqlite.txt` shows zero lines changed, and the same for the other three pairs

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run the full store conformance in pkg/store/storetest on both dialects and attach the before and after name lists. Confirmed by: CI (go test on SQLite and PostgreSQL). Check: `go test -timeout 60m ./pkg/store/storetest/` passes
- [ ] 3.2 Run the lint and test gate for the moved suites. Confirmed by: the driver's gates. Check: `make check` with zero findings
