## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Edit `internal/servertest/roundtrips_test.go:935` to set failing budgets lock 42 and unlock 10; BLOCKED by TFE workspace lock route Check: `go test ./internal/servertest -run TestRoundTripsOfRequests -count 1` fails on main with lock 45 and unlock 11; BLOCKED by TFE workspace lock route

## 2. Fix

- [ ] 2.1 Edit `pkg/store/postgres/workspaces.go:1513` and `pkg/store/postgres/internal/db/queries.sql.go:7136` to pass the stable unit ID to both ListGateObjectsOfScope reads and fold the last-release write into the lock and unlock saves in both dialects; BLOCKED by TFE workspace lock route Check: `go test ./internal/servertest -run TestRoundTripsOfRequests -count 8` passes; BLOCKED by TFE workspace lock route

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run `e2e/ha_test.go:38` with the real Pulumi CLI and OpenTofu, then run make check; BLOCKED by TFE workspace lock route Check: `E2E_INSTANCES=3 make e2e` passes on `e2e/ha_test.go:38` plus `make check` with zero findings; BLOCKED by TFE workspace lock route
