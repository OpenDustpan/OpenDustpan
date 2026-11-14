---
version: 1
---

Issue: #1260, #1270.

## Problem

A site administrator deletes a principal and expects its saved views to go away and restores to respect the same view caps as create.

## Evidence

- RemoveOwner exists and only its definition matches. Proof: rg -n -F 'RemoveOwner' pkg internal cmd -> pkg/savedview/service.go:190:// RemoveOwner deletes the views of the principal with the ID owner in every and pkg/savedview/service.go:201:func (s *Service) RemoveOwner(ctx context.Context, owner string) (int, error) {
- Principal delete path exists on main. Proof: pkg/principal/service.go:254 (func Service.DeletePrincipal)
- Restore staged-view check exists in both dialects. Proof: pkg/store/sqlite/backup_saved_views.go:40:func checkStagedSavedViews(ctx context.Context, conn *sql.DB) error { and pkg/store/postgres/backup_saved_views.go:30:func checkStagedSavedViews(ctx context.Context, pool pgQuerier, staging string) error {
- Caps are constants enforced on create. Proof: pkg/savedview/savedview.go:44 MaxPerOwner and pkg/savedview/savedview.go:47 MaxPerNamespace; pkg/store/sqlite/savedviews.go:83:if inNamespace > savedview.MaxPerNamespace || ofOwner > savedview.MaxPerOwner {
- Staged restore failures use backup.ErrCorrupt in the storetest suite. Proof: pkg/store/storetest/restore.go:750:if err := pdst.ApplyStaging(ctx); !errors.Is(err, backup.ErrCorrupt) { and pkg/store/storetest/restore.go:1279:if err := s.ApplyStaging(t.Context()); !errors.Is(err, backup.ErrCorrupt) {

## Fix boundary

- Start a retried durable operation after principal delete that calls savedview.Service.RemoveOwner outside the delete transaction, per namespace with its own fence and per-view ID-only audit.
- Fail staged restore with backup.ErrCorrupt when staged saved views exceed MaxPerOwner for one owner or MaxPerNamespace for one namespace, in sqlite and postgres, and document the rule.

Out of scope:

- Sharing of saved views with readers or collections.
- Admin view:manage delete path.
- Other staged tables.

## Check

- Add failing service test in pkg/savedview/service.go:201 path area covering delete of principal with views in two namespaces, run of RemoveOwner, per-view ID-only audit. Check: TestPrincipalCleanupDeletesViewsInTwoNamespaces fails on main, passes after.
- Add failing storetest backup conformance cases in pkg/store/storetest/restore.go:750 area for MaxPerOwner+1 and MaxPerNamespace+1 in both dialects. Check: TestStagedSavedViewsOverOwnerCap and TestStagedSavedViewsOverNamespaceCap fail on main for sqlite and postgres, pass after.
- Wire durable operation from principal delete commit in pkg/principal/service.go:254 to RemoveOwner in pkg/savedview/service.go:201 with retry of 30s then 5min then 60min up to 50 attempts; keep per-namespace fence and per-view audit. Check: Delete principal then list views in both namespaces shows zero views of the owner.
- Extend checkStagedSavedViews in pkg/store/sqlite/backup_saved_views.go:40 and pkg/store/postgres/backup_saved_views.go:30 to count staged rows grouped by owner plus namespace and by namespace and return backup.ErrCorrupt above any cap. Check: ApplyStaging on over-cap staging returns backup.ErrCorrupt in sqlite and postgres.
- Add the NEW file docs/guides/backup-restore with the fail-closed cap rule and filtered-backup repair. Check: Guide states restore fails on over-cap staged views and the repair.
- E2E against real stores through existing principal and view routes with access token of a site administrator. Check: DELETE ANY /api/server/principals/{} then GET /api/dustpan/v1/namespaces/{}/views shows cleanup with access token of site administrator.
- Usability check of delete-then-cleanup and refused-restore errors. Check: Usability walkthrough with site administrator from docs page alone completes with no surprise.
- Run full check with zero findings. Check: `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- DeletePrincipal deletes a principal. (`pkg/principal/service.go:254 (func Service.DeletePrincipal)`)
- RemoveOwner deletes the views of one owner in every namespace. (`pkg/savedview/service.go:201 (func Service.RemoveOwner)`)
- MaxPerOwner and MaxPerNamespace bound views. (`pkg/savedview/savedview.go:44 (const MaxPerOwner)`)
- checkStagedSavedViews checks staged saved-view rows in both dialects. (`pkg/store/postgres/backup_saved_views.go:30 (func checkStagedSavedViews)`)
- Create refuses over-cap views with savedview.ErrLimit. (`pkg/savedview/savedview.go:69 (var savedview.ErrLimit)`)
- ErrCorrupt sentinel exists for corrupt staged backups. (`pkg/backup/backup.go:350 (func ParseID)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 266: A saved view is a server object with an API** (not owner-tagged).

- Decisions that apply: 2 (current), 5 (current), 8 (current), 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A view has one owner: one person in one namespace. The owner is the ID of the principal that made the view"
- Quote: "The limits are constants, not settings. A name has 1 to 128 characters."
- Quote: "The cleanup does not run in the transaction that deletes the principal."
- Quote: "the table of views joins the backup table list, a restore checks each row again"
- Effect: Cleanup runs after principal delete outside the delete transaction with per-namespace fence and per-view audit by ID. Restore refuses staged views over MaxPerOwner or MaxPerNamespace with backup.ErrCorrupt.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does RemoveOwner run after principal delete? Enqueue a record in the existing durable-operations table in the same delete transaction, execute after commit. ADR 266 decision 8 forbids execution in the delete transaction and names a durable operation on the primary as the example.
- Fail restore or warn when staged views exceed caps? Fail the restore with backup.ErrCorrupt, like a staged row that fails view rules. ADR 266 decision 9 says restore checks each row again and decision 5 states caps as constants.
- Where is the cap count enforced? Count in the same staged check query in both dialects, with storetest backup conformance cases for both caps in sqlite and postgres.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
