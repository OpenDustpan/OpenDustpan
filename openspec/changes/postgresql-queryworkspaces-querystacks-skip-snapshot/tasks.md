## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing swap-mid-read conformance cases with a fault hook between the page select and the tag select in NEW pkg/store/storetest/stack_workspace_swap_read_test.go. Check: NEW storetest swap-mid-read cases for QueryStacks and QueryWorkspaces fail on main
- [ ] 1.2 Add a guard test in NEW pkg/store/postgres/snapshot_guard_test.go that fails on direct RepeatableRead, BeginTx, or s.queries pool reads in page files outside Store.snapshotOptions. Check: NEW TestSnapshotGuardFailsOnDirectSnapshotRead fails on main

## 2. Fix

- [ ] 2.1 Route QueryWorkspaces through Store.inSnapshotConn with snapshotOptions and readConflict in `pkg/store/postgres/workspace_page.go:15`. Check: go test ./pkg/store/postgres/ ./pkg/store/storetest/
- [ ] 2.2 Route QueryStacks and stackRange page selects plus ListStackTagsOfStacks through Store.inSnapshotConn in `pkg/store/postgres/stack_page.go:17`. Check: go test ./pkg/store/postgres/ ./pkg/store/storetest/
- [ ] 2.3 Route the sqlite stack page through Store.inReadTx in `pkg/store/sqlite/stack_page.go:37`. Check: go test ./pkg/store/sqlite/ ./pkg/store/storetest/
- [ ] 2.4 Route inventory QueryUnits through Store.inReadTx in `pkg/store/postgres/inventory_query.go:51` and its sqlite counterpart. Check: go test ./pkg/store/postgres/ ./pkg/store/sqlite/
- [ ] 2.5 Verify Handler.listWorkspacePage maps namespace.ErrUnavailable to 503 try again in `internal/api/tfe/workspaces.go:459`. Check: go test ./internal/api/tfe/ with forced namespace.ErrUnavailable from QueryWorkspaces shows 503

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire check: call GET /api/v2/organizations/{}/workspaces with an access token against the real server during a staged swap in `internal/api/tfe/workspaces.go:459`; each page answers one snapshot or 503. Check: `GET /api/v2/organizations/{}/workspaces` with an access token during a staged restore swap. Confirmed by: CI (make e2e).
- [ ] 4.2 Run the definition of done: route authorization unchanged, docs page for the 503 retry behavior, usability check, make check with zero findings. Check: `make check` with zero findings. Confirmed by: CI (make check).
