## 1. Tests first

- [x] 1.1 Add the hostile handler tests TestPlacementRuleRefusesCollectionName, TestPlacementRuleRefusesMalformedID and TestPlacementRuleWriteNeedsTheAction to the existing file internal/api/inventory/placement_routes_test.go, and TestCollectionListAnswersTheID to the existing file internal/api/inventory/collections_test.go. Check: `go test ./internal/api/inventory/ -run 'TestPlacementRule|TestCollectionListAnswersTheID' -count=1` fails on main
- [x] 1.2 Add the store cases TestPlacementRuleRefusesForeignCollection and TestPlacementRuleOfRemovedCollectionPlacesNothing to the conformance suite in pkg/collection/collectiontest, run by both dialects. Check: `go test -timeout 60m ./pkg/store/... -run PlacementRule -count=1` fails on main

## 2. Build

- [x] 2.1 Replace `collection.Rule.Collection Name` (pkg/collection/placement.go:189) with the NEW field `CollectionID string`, checked by `Rule.Validate`, and change the placement service in pkg/collection/placement_service.go to place by the ID. Check: `go test ./pkg/collection/... -count=1` passes
- [x] 2.2 Edit the table `placement_rules` in place in pkg/store/sqlite/migrations/0168_baseline.sql (:1307) and pkg/store/postgres/migrations/0168_baseline.sql: `collection_id` with a foreign key to `collections (id)` in place of `collection_key`; update both `.sql.sha256` files and the queries of the placement rules in pkg/store/sqlite/queries.sql and pkg/store/postgres/queries.sql, then run `make generate` (sqlc). The table is in the portable backup list pkg/backup/portable_tables.go: change its column there. Check: the suite of task 1.2 passes on both dialects and `sqlc diff` is clean
- [x] 2.3 In internal/api/inventory/placement.go, take `collectionId` in `placementRuleRequest` (:99), refuse a body with `collection`, and answer `collectionId` and the current name `collection` in `placementRuleEntry` (:81); in internal/api/inventory/collections.go answer `id` in `collectionEntry` (:110). Add the NEW codes `placement.collection-id-required` and `placement.invalid-collection-id` to internal/errcode/catalog_placement.go. Run `make generate` and `make clients-generate` for internal/api/apidesc/openapi.json and the clients. Check: the tests of task 1.1 pass

## 3. Definition of done

- [x] 3.1 Update the existing guide docs/guides/collections-and-placement.md with the ID in the answers and in the placement rules. Check: `make docs-lint` passes
- [ ] 3.2 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
