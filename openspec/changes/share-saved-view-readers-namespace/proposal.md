---
version: 1
---

Issue: #1203, #1204, #1205, #1206.

## Problem

A developer filters the unit list and wants to open the same list again and give it to the team. Today a saved view is private to its maker, so each teammate repeats the filter by hand.

## Evidence

- Main has a delete route handler for saved views. Proof: internal/api/inventory/views.go:520 (func Handler.deleteView)
- Main stores the view query through an encoder in the store package. Proof: pkg/store/internal/viewstore/viewstore.go:34 (func EncodeQuery)
- Main has the unit list handler that the view units route reuses. Proof: internal/api/inventory/inventory.go:476 (func Handler.serveUnitList)
- Main has a saved view store adapter for PostgreSQL. Proof: pkg/store/postgres/savedviews.go:23 (func Store.SavedViews)
- The unit list has no sort, no choice of columns and no states filter. Proof: docs/rfc/0100-saved-views.md (section What exists today)

## Fix boundary

- Add visibility private, namespace and collection to a saved view. The collection scope stores the stable collection ID.
- Add the NEW action view:share, held through a stored grant on the namespace or the collection, and the NEW route PUT /api/dustpan/v1/namespaces/{namespace}/views/{view_id}/visibility with audit entries.
- Add rename and replace of the name and query with If-Match, the fence and an audit entry. Only the maker may do it. A stale If-Match answers 412 and a missing view answers 404.
- Let a namespace administrator delete any view of the namespace, with an audit entry.

Out of scope:

- Cleanup of the private views of a removed principal: the change run-cleanup-saved-views-when keeps this work.
- The status code of a stale If-Match as a general rule: the change api-handlers-409-vs-412 sets it for all handlers. This change follows 412 for views.
- Sort and columns of the unit list and of saved views (issue 1204): the list API part does not exist on main.
- State filter of saved views (issue 1205): it waits for the states filter of the unit list.
- Collection create, move and delete rules
- Console screens for views
- A view as the object of a grant

## Check

- Write failing domain tests in pkg/savedview/savedviewtest for visibility, collection ID, unshare, replace with an invalid query and old rows. Check: TestViewShareNamespace and TestViewRowFailsValidateOnRead fail on main
- Write failing handler tests for internal/api/inventory/views.go: share, outsider 404, 403 without view:share, stale If-Match 412, delete by a namespace administrator, using an access token. Check: TestViewUnitsPerReader fails on main
- Add visibility to pkg/savedview/savedview.go and the columns in pkg/store/postgres/savedviews.go and the sqlite baseline. Check: `go test -race ./pkg/savedview/...` passes
- Add the visibility route and the replace route in internal/api/inventory/views.go with view:share, the fence, Idempotency-Key, If-Match and audit entries. Add the collection-delete reset to private in pkg/store/postgres/savedviews.go. Check: `go test -race ./internal/api/inventory/...` passes
- Add an e2e step in e2e/saved_views.go that calls POST /api/dustpan/v1/namespaces/{namespace}/views and the NEW PUT visibility route with an access token (gate: view:create and view:share), then GET the view units route as a second principal with its own access token. Check: `make e2e` passes the saved view step
- Add the docs page for saved views and run the usability check from the page alone. Check: The docs commands run as written in a test
- Run the full checks. Check: `make check` reports zero findings

Depends on:

- run-cleanup-saved-views-when
- `run-cleanup-saved-views-when` (order of the milestone review)
- `api-handlers-409-vs-412` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main deletes a view through the inventory views handler. (`internal/api/inventory/views.go:520 (func Handler.deleteView)`)
- Main encodes the view query for the store. (`pkg/store/internal/viewstore/viewstore.go:34 (func EncodeQuery)`)
- Main stores saved views through the PostgreSQL store. (`pkg/store/postgres/savedviews.go:23 (func Store.SavedViews)`)
- Main serves the unit list that a view query reuses. (`internal/api/inventory/inventory.go:476 (func Handler.serveUnitList)`)
- Main has an e2e helper for saved view store options. (`e2e/saved_views.go:13 (func savedViewStoreOptions)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 51: Collections: a named set of units in a namespace** (not owner-tagged).

- Decisions that apply: 7 (current), 8 (current).
- Changed by: ADR 61 extends decision 5; ADR 248 changes decision 2; ADR 311 extends; ADR 346 extends decision 6. Chain to the end: ADR 61, ADR 248, ADR 311, ADR 346, ADR 329, ADR 286.
- Current rule: this ADR, with ADR 61, 248, 311, 346.
- Quote: "Labels never grant access. A label (a tag that a tool sends, or a label of OpenDustpan) is for views and filters. A saved view can look like a collection, but it grants nothing."
- Quote: "A collection is an object of a grant."
- Effect: A shared view grants no access. The units route checks unit:read for each unit with the access of the reader. The action view:share is checked on the namespace, or on the collection for a collection scope. A view is not an object of a grant.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a shared view grant read access to its units? No. ADR 51 decision 7: a saved view grants nothing. The units route runs the query again with the access of the reader.
- How does a collection scope survive a rename of the collection? The view stores the stable collection ID (ADR 362 rule for stored references).
- Is a view an object that a grant names? No. view:share stays an action on the namespace or the collection. ADR 51 decision 8 lets the collection widen the object.
- Who holds view:share by default? Nobody by code. The namespace administrator role holds view:share through a stored grant, and the maker may set visibility of their own view through the same action. The object is the namespace, or the collection for a collection scope.
- Does the default collection change this work? Owner decision, 13 Nov 2026 (queue P5): a new namespace gets a stored default collection. A collection scope names any collection by ID, so no special rule is needed.
- Who can unshare, and who can edit a shared view? The maker and a holder of view:share set any visibility, including private. Rename and replace need the maker only. A namespace administrator can delete any view but cannot edit content.
- When does the visibility route answer 403 and when 404? A caller who cannot read the view gets 404. A caller who can read it but lacks view:share gets 403.
- What happens to a view whose collection is deleted or whose units move? Deleting the collection sets the visibility of its views to private in the same transaction, with an audit entry. A move of a unit changes no view: the units route checks unit:read for each unit.
- Do the private views of a removed principal stay? The change run-cleanup-saved-views-when answers this. This change does not specify it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
