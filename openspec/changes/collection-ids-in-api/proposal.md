---
version: 1
---

Short form: a missing stable ID in the API. It is a prerequisite of
cli-collections-and-placement-commands (#2623).

## Problem

**Persona: namespace administrator.** An administrator scripts the placement
of new stacks and workspaces into collections. ADR 362 (owner) says that each
stored reference to an entity that a user can rename, or remove and make
again, uses the stable ID. On main, the collection list and read answer no
collection ID, and a placement rule names its collection by name: the rule
stores the name key, so a collection that is removed and made again under the
same name takes over the rules of the old one, and a script cannot name a
collection by an ID that does not change.

## Evidence

- `collection.Collection` holds the stable `ID` with the prefix `col-`
  (pkg/collection/collection.go:126-133), but `collectionEntry`
  (internal/api/inventory/collections.go:110) answers only namespace, name and
  createdAt.
- `collection.Rule.Collection` is a `Name` (pkg/collection/placement.go:189).
- `placementRuleRequest.Collection` takes a name
  (internal/api/inventory/placement.go:99), and `placementRuleEntry` answers it
  (:81).
- The table `placement_rules` stores `collection_key` with a foreign key to
  `collections (namespace, name_key)` (pkg/store/sqlite/migrations/0168_baseline.sql:1307-1317;
  the PostgreSQL baseline has the same table).

## Fix boundary

- The collection list and read answer `id` (the stable `col-` ID).
- A placement rule create and update take `collectionId` and refuse a body
  that names `collection`; the stored rule holds the collection ID.
- The rule read and list answer `collectionId` and, as a label for people
  only, `collection` (the current name of that collection).
- The table `placement_rules` holds `collection_id` with a foreign key to the
  ID of `collections`, edited in place in the baseline of each dialect.
- No migration and no alias: no tag shipped the name shape (no data, no tag).

## Contract

- `GET /api/dustpan/v1/namespaces/{namespace}/collections` and
  `GET /api/dustpan/v1/namespaces/{namespace}/collections/{collection}`
  (action `namespace:read`, unchanged): each entry is
  `{"id": "col-…", "namespace": "acme", "name": "web", "createdAt": 1700000000000}`.
- `POST /api/dustpan/v1/namespaces/{namespace}/placement-rules` and
  `PUT /api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}` (action
  `namespace:manage-placement-rules`, unchanged): the body is
  `{"tool": "pulumi", "project": "…", "stack": "…", "collectionId": "col-…", "position": 1}`.
  A body with `collection` and no `collectionId` answers 400 with the NEW
  error code `placement.collection-id-required`.
  An ID that names no collection of the namespace answers 404
  `collection.not-found`. A malformed ID answers 400 with the NEW code
  `placement.invalid-collection-id`.
- `GET …/placement-rules` and `GET …/placement-rules/{rule}` (action
  `namespace:read-placement-rules`, unchanged): each rule is
  `{"id": "plr-…", "namespace": "acme", "position": 1, "tool": "pulumi", "project": "…", "stack": "…", "collectionId": "col-…", "collection": "web", "createdBy": "…", "createdAt": 1700000000000}`.
  `collection` is the current name, read at each request, a label only.
- Go: `collection.Rule.CollectionID string` (NEW) replaces
  `collection.Rule.Collection Name`; `collection.Rule.Validate` returns
  `collection.ErrInvalidName` for an ID without the `col-` form, and the store
  returns `collection.ErrNotFound` for an ID of no collection of the namespace.
- Stored schema, each dialect: `placement_rules.collection_id TEXT NOT NULL`
  with a foreign key to `collections (id)`, in place of `collection_key`.

## Layers (defense in depth)

Placement decides the collection, and so the grants, of each new unit.

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary check | The handler accepts only `collectionId` of the `col-` form and refuses `collection` in a write body. | TestPlacementRuleRefusesCollectionName, TestPlacementRuleRefusesMalformedID |
| encode or contain | SQL placeholders; the ID is never put into a query string. | covered by the store suite |
| limit | The rule names one collection of the same namespace: the store refuses an ID of another namespace. | TestPlacementRuleRefusesForeignCollection (store suite, both dialects) |
| authorize at point of use | `namespace:manage-placement-rules` on the namespace of the path, unchanged. | TestPlacementRuleWriteNeedsTheAction |
| fail closed | An unknown ID, or a collection removed after the rule was made, places no unit: the placement answers its refusal and writes the audit entry. | TestPlacementRuleOfRemovedCollectionPlacesNothing |

## Check

`go test ./internal/api/inventory/... ./pkg/collection/... -count=1` passes,
the store suite passes on both dialects, and `make e2e` passes.

## ADR check

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged, decision 1;
Changed by: none).
- Quote: "Every stored reference to an entity uses the stable ID of the entity
  when a user can rename the entity, or delete and recreate it. A name is a
  label for people only."
- Effect: the rule stores the collection ID, and the name is an answer label.

## Open questions

none
