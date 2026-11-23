## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a test in NEW file internal/store/postgres/swap_wait_test.go that the swap logs one warning after 500 ms and none before 500 ms. Check: `go test -timeout 60m ./internal/store/postgres -run TestSwapWaitLogsOnce -count=1` fails on main
- [ ] 1.2 Set the swap non-queue bound to 500 ms in the test that calls checkSwapDoesNotQueueReads, in NEW file internal/store/postgres/swap_bound_test.go. Check: `go test -timeout 60m ./internal/store/postgres -run TestSwapNonQueueBound -count=1` fails on main
- [ ] 1.3 Add a test in NEW file internal/store/postgres/inventory_query_labels_test.go of QueryUnits labels (internal/store/postgres/inventory_query.go, func Store.QueryUnits) in one statement with empty and ordered labels. Check: `go test -timeout 60m ./internal/store/postgres -run TestQueryUnitsLabelsOneStatement -count=1` fails on main
- [ ] 1.4 Add a test in NEW file internal/store/postgres/state_at_conflict_test.go that StateAt (internal/store/postgres/state_at.go:20, func Store.StateAt) and WorkspaceStateAt begin and read conflicts map to ErrUnavailable. Check: `go test -timeout 60m ./internal/store/postgres -run TestStateAtConflictMapsUnavailable -count=1` fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Confirm that no request flow changed, so the round-trip budgets stand.

## 3. Definition of done

- [ ] 3.1 Implement the four changes in internal/store/postgres/maintenance_revocations.go (swap wait warning), internal/store/postgres/inventory_query.go (QueryUnits labels) and internal/store/postgres/state_at.go (StateAt and WorkspaceStateAt conflict mapping). Add the swap wait to the existing docs page docs/guides/high-availability.md. The change adds no route. The existing restore routes keep their access token authorization. Check: `make check` with zero findings. `make docs-lint` passes on each edited guide.
