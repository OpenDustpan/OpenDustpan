---
version: 1
---

## Problem

Services in pkg/ widen the SDK surface. The Embedder can build a service by hand and skip the server wiring. Services move to internal/ so that pkg/ holds only ports, types, sentinels and suites.

## Evidence

- The three internal service packages do not exist on main. Proof: The paths list of the pack shows each of the three NEW packages with exists false.
- PlacementService lives in pkg/collection on main. Proof: The main facts of the pack: type PlacementService at pkg/collection/placement_service.go:36.
- The settings service is built in pkg/dustpan on main. Proof: The main facts of the pack: func newSettingService at pkg/dustpan/settings_resource.go:77.
- The reconciler has a config test in pkg/job on main. Proof: The main facts of the pack: test TestNewReconcilerChecksItsConfig at pkg/job/reconcile_test.go:40.

## Fix boundary

- Add the NEW packages internal/settingsvc, internal/collectionsvc and internal/jobsvc.
- Move setting.Service, PlacementService and Reconciler into them, with their tests.
- Keep only ports, types, sentinels and suites in pkg/setting, pkg/collection and pkg/job.
- Change pkg/dustpan to build the services from the NEW packages.
- Keep the default collection and the catch-all placement rule of a new namespace unchanged.

Out of scope:

- Move of trigger services
- Move of gate config service and ApproverDirectory
- Move of principal guard and built-in adapters
- Any change of behavior, route or error code

## Check

- Add the new file internal/settingsvc/boundary_test.go with a test that pkg/setting, pkg/collection and pkg/job export no service constructor. Check: go test ./internal/settingsvc/... fails on main. Confirmed by: CI (go test).
- Add the new file internal/collectionsvc/default_collection_test.go with a test that a new namespace has the default collection and the catch-all placement rule after create. Check: go test ./internal/collectionsvc/... fails on main. Confirmed by: CI (go test).
- Move pkg/collection/placement_service.go to internal/collectionsvc, the settings service to internal/settingsvc, and the reconciler with pkg/job/reconcile_test.go to internal/jobsvc. Check: go test ./internal/settingsvc/... ./internal/collectionsvc/... ./internal/jobsvc/... Confirmed by: CI (go test).
- Wire the services in pkg/dustpan/settings_resource.go. Run the e2e step that calls GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token. Update the SDK docs page. Check the usability of the Embedder path from the docs page. Check: make e2e and make check with zero findings. Confirmed by: CI (make check).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The placement service lives in pkg/collection. (`pkg/collection/placement_service.go:36 (type PlacementService)`)
- The settings service is built in pkg/dustpan. (`pkg/dustpan/settings_resource.go:77 (func newSettingService)`)
- The reconciler config test lives in pkg/job. (`pkg/job/reconcile_test.go:40 (func TestNewReconcilerChecksItsConfig)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR govern where services live? The pack lists no ADR for this item. No decision text is available to quote, so no ADR entry is made.
- Does the move change the default collection of a new namespace? No. Owner decision, 13 Nov 2026 (queue P5): a new namespace gets a stored, visible, audited default collection and a catch-all placement rule in the create transaction. An admin can delete either. The create request can opt out. The move keeps this and a scenario tests it.
- How do Embedders migrate? Before the first tag, the project allows a breaking SDK change and writes no compatibility code. An Embedder uses dustpan.NewServer, which wires the services.
- Does the move change a route or an error code? No. Routes, error codes and audit entries stay the same.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
