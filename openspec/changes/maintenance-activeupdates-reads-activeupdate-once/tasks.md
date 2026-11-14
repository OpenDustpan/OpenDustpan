## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing conformance cases for NEW ListActiveUpdates in pkg/store/storetest/updates.go:97, ordered by stack ID with empty-namespace, deleted-stack, and failure cases. Check: `go test ./pkg/store/storetest -run TestActiveUpdatesBatch -count=1` fails on main (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)
- [ ] 1.2 Add failing round-trip cases for paged backup and restore reads in internal/servertest/roundtrips_test.go:1144 with 3 and 30 stacks. Check: `go test ./internal/servertest -run TestRoundTripsOfBackup -count=1` fails on main (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)

## 2. Fix

- [ ] 2.1 Implement NEW ListActiveUpdates in both store adapters (pkg/store/sqlite, pkg/store/postgres) and use it in activeUpdates in pkg/maintenance/maintenance.go:674. Check: `go test ./pkg/store/... -count=1` passes (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)
- [ ] 2.2 Page backup and restore state-version reads by id cursor with page size 200 in pkg/backup/portable_read.go:54 (func ReadPortableTable) and the restore copy path, read each workspace and secret key once, and remove the allowance in TestRoundTripsOfBackup. Check: `go test ./internal/servertest -run TestRoundTripsOfBackup -count=1` passes with no allowance (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the batch read through the existing route ANY/POST /api/dustpan/v1/maintenance/enter with its gate and access token, with no new route. Check: The existing route ANY/POST /api/dustpan/v1/maintenance/enter keeps maintenance.busy on active work and maintenance.unavailable on batch failure with an access token and its maintenance gate
- [ ] 4.2 Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings.
