---
version: 1
---

Issue: #2211.

## Problem

A failed export leaves no record, pages can disagree when collection membership changes between pages, and a very large collection makes one very large query.

## Evidence

- The postgres store lists every unit of a collection with no limit argument in the pack. Proof: pkg/store/postgres/collections.go:238 (func Store.ListCollectionUnitIDs)
- The sqlite store lists the units of a collection too, so both dialects share the port and the fix sits in the access service. Proof: pkg/store/sqlite/collections.go:253 (ListCollectionUnitIDs)
- The collection review scope exists on main. Proof: pkg/access/review_service.go:550 (func Service.collectionReviewScope)
- The review route of a collection exists on main. Proof: route ANY /api/server/namespaces/{}/collections/{}/access-review in the pack
- The namespace review route exists on main. Proof: route ANY /api/server/namespaces/{}/access-review in the pack
- The error code `access.collection-unit-cap` is listed on main, so this change reuses it and adds no code. Proof: internal/errcode/catalog_access.go:6

## Fix boundary

- Write one audit entry of kind `access` for each collection access review request that fails after the authorization check. Each page is one request and writes its own entry on failure.
- Read the unit IDs of the collection once at the first page and keep them in the signed page token, so every page of one export uses the same list.
- Refuse a collection of more than 2000 units with the existing error code `access.collection-unit-cap`, status 422. The refusal text and the docs page name the namespace access review route as the scope to use.

Out of scope:

- Other access review scopes
- A new collection membership API
- Console changes

## Check

- Write the failing test of the failed-export audit in NEW file pkg/access/review_collection_audit_test.go. Check: `go test ./pkg/access -run TestCollectionReviewFailedExportWritesAudit` fails on main.
- Write the failing test of the snapshot in NEW file pkg/access/review_collection_snapshot_test.go. Check: `go test ./pkg/access -run TestCollectionReviewPagesFromSnapshot` fails on main.
- Write the failing test of the cap, with the cases 2000, 2001 and the default collection over the cap, in NEW file pkg/access/review_collection_cap_test.go. Check: `go test ./pkg/access -run TestCollectionReviewCap` fails on main.
- Write the audit entry on failure and build the snapshot and the cap in pkg/access/review_service.go. Check: The three tests pass.
- Use the existing code `access.collection-unit-cap` from internal/errcode/catalog_access.go for the cap answer, with the text that names the namespace review route. Check: `go test ./internal/errcode` passes.
- Add an e2e step that calls the route ANY /api/server/namespaces/{}/collections/{}/access-review with an access token of a site administrator, through the read-right gate on the collection, for a missing collection and for a paged export. Confirmed by: CI (make e2e). Check: `make e2e` passes the step against the real server.
- Add the docs page of the export, with the cap error, the namespace review route as the next command, and run the usability check from the page alone. Confirmed by: CI (make docs-lint). Check: `make docs-lint` has zero findings.
- Run the full check. Confirmed by: CI (make check). Check: `make check` has zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The collection review scope exists. (`pkg/access/review_service.go:550 (func Service.collectionReviewScope)`)
- The review query builder exists. (`pkg/access/review_service.go:765 (func reviewQuery)`)
- The store lists unit IDs of a collection. (`pkg/store/postgres/collections.go:238 (func Store.ListCollectionUnitIDs)`)
- A test of the collection review exists. (`pkg/access/review_collection_test.go:65 (func TestReviewOfACollection)`)
- The error code of the cap exists. (`internal/errcode/catalog_access.go:6`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide the audit, snapshot or cap of this export? No ADR is in the pack for these points, so no decision is touched.
- Does Owner decision, 13 Nov 2026 (queue P5) apply? Yes, in effect. A new namespace gets a stored catch-all default collection. In a namespace of more than 2000 units that collection is over the cap, and its review is refused. The decision stays as it is: the admin can delete the collection, and the refusal text and the docs name the namespace access review route as the scope for such a namespace. A scenario covers the default collection over the cap.
- Where does the snapshot live across HTTP requests? In a signed page token, not in server state. No expiry, restart or replica issue arises.
- Is the grant list part of the snapshot? No. Only unit IDs. The issue says the grant snapshot is already stable.
- Does a failure before authorization write an audit entry? No. A caller without the read right gets a denial and no entry, so a hidden or missing collection leaks nothing and a caller cannot flood the audit log.
- What if the audit write fails? The request fails closed with 500.
- Do the grants of a unit make the query large? The cap bounds the unit parameter at 2000. The existing query reads the grants and they are not part of the cap.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
