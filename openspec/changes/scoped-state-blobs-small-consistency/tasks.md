## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test that servicesOver Refusals stays empty in `pkg/store/storetest/workspaces.go` Check: `go test ./pkg/store/storetest -run TestServicesOverRefusalsEmpty -count=1` fails on main
- [ ] 1.2 Add failing test that OpenStateBlob errors omit the key in `pkg/backup/service.go` Check: `go test ./pkg/backup -run TestOpenStateBlobHidesKey -count=1` fails on main
- [ ] 1.3 Add hostile List test for DeletePurgedBlobs and sweepUnreferenced in `pkg/workspace/service.go` Check: `go test ./pkg/workspace -run TestDeletePurgedBlobsRefusesForeignListKey -count=1` fails on main
- [ ] 1.4 Add purge namespace ID test in NEW `pkg/store/storetest/blob_purge_namespace_test.go` Check: `go test ./pkg/store/storetest -run TestBlobPurgeKeepsNamespaceID -count=1` fails on main

## 2. Fix

- [ ] 2.1 Edit the baseline of each dialect for blob_purges namespace_id in `pkg/store/sqlite/migrations` and `pkg/store/postgres/migrations`, regenerate sqlc Check: `make generate && git diff --exit-code` passes
- [ ] 2.2 Refuse foreign list keys and share cleanupContext in `pkg/workspace/stateblobs.go` and `pkg/secret/itemkeyblob.go` Check: `go test ./pkg/workspace ./pkg/secret -count=1` passes
- [ ] 2.3 Add empty-workspace guard in `pkg/workspace/service.go` Check: `go test ./pkg/workspace -run TestAuditOutOfScope -count=1` passes
- [ ] 2.4 Replace tofu/ examples with namespaces/<namespace ID>/ in `pkg/blob/cache/cache.go` and `pkg/blob/blobtest/scope.go` Check: `rg -n 'tofu/' pkg/blob/cache pkg/blob/blobtest` returns no hits

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Reset dev data after baseline edit and run affected suites Check: `opendustpan dev reset` then `go test ./pkg/workspace ./pkg/backup ./pkg/store/storetest -count=1` passes
- [ ] 4.2 Docs page updated, usability check from the docs page alone Check: `make check` with zero findings
