## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service test in pkg/savedview/service.go:201 path area covering delete of principal with views in two namespaces, run of RemoveOwner, per-view ID-only audit. Check: TestPrincipalCleanupDeletesViewsInTwoNamespaces fails on main, passes after.
- [ ] 1.2 Add failing storetest backup conformance cases in pkg/store/storetest/restore.go:750 area for MaxPerOwner+1 and MaxPerNamespace+1 in both dialects. Check: TestStagedSavedViewsOverOwnerCap and TestStagedSavedViewsOverNamespaceCap fail on main for sqlite and postgres, pass after.

## 2. Implementation

- [ ] 2.1 Wire durable operation from principal delete commit in pkg/principal/service.go:254 to RemoveOwner in pkg/savedview/service.go:201 with retry of 30s then 5min then 60min up to 50 attempts; keep per-namespace fence and per-view audit. Check: Delete principal then list views in both namespaces shows zero views of the owner.
- [ ] 2.2 Extend checkStagedSavedViews in pkg/store/sqlite/backup_saved_views.go:40 and pkg/store/postgres/backup_saved_views.go:30 to count staged rows grouped by owner plus namespace and by namespace and return backup.ErrCorrupt above any cap. Check: ApplyStaging on over-cap staging returns backup.ErrCorrupt in sqlite and postgres.
- [ ] 2.3 Add the NEW file docs/guides/backup-restore with the fail-closed cap rule and filtered-backup repair. Check: Guide states restore fails on over-cap staged views and the repair.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 E2E against real stores through existing principal and view routes with access token of a site administrator. Check: DELETE ANY /api/server/principals/{} then GET /api/dustpan/v1/namespaces/{}/views shows cleanup with access token of site administrator.
- [ ] 4.2 Usability check of delete-then-cleanup and refused-restore errors. Check: Usability walkthrough with site administrator from docs page alone completes with no surprise.
- [ ] 4.3 Run full check with zero findings. Check: `make check` reports zero findings.
