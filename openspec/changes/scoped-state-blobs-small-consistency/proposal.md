---
version: 1
---

Issue: #1279, #1284.

## Problem

A Developer keeps state blobs isolated across namespaces and workspaces. The Developer runs the scoped-blob tests, inspects purge rows, and reads sweep refusals. Stale tofu/ examples, key material in errors, api-channel sweep refusals, split cleanup contexts, missing namespace ID on purge rows, and missing hostile list tests make that isolation hard to trust.

## Evidence

- storetest helper servicesOver exists and shares one blob store Proof: pkg/store/storetest/workspaces.go:4191: func servicesOver(t *testing.T, s WorkspaceStore, n int) []*workspace.Service
- Refusals recorder exists for scopeaudit Proof: pkg/workspace/workspacetest/refusals.go:21: type Refusals struct {
- OpenStateBlob is part of the backup state opener Proof: pkg/backup/service.go:217: OpenStateBlob(ctx context.Context, key blob.Key) (io.ReadCloser, error)
- cleanupContext helper exists Proof: pkg/workspace/write_state.go:529: func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc)
- blob_purges table exists in backup code Proof: pkg/backup/backup.go:136: "blob_purges",
- WriteOncePrefixes has no caller outside its definition Proof: rg -n -F --glob '!*_test.go' 'blobcache.WriteOncePrefixes' pkg internal cmd: 0 matches

## Fix boundary

- blob_purges rows store namespace ID alongside namespace reference
- Hostile List tests for DeletePurgedBlobs and sweepUnreferenced with a foreign workspace key
- OpenStateBlob and related error texts omit key material
- Background sweep refusals use background-work channel, not api
- Shared cleanupContext between stateBlobs.refuse and secret.ItemKeyBlobs.refuse
- blob/cache and blobtest examples no longer use tofu/ layout; list guard added

Out of scope:

- Namespace delete and re-create lifecycle beyond purge-row namespace ID
- New blob layouts or new cache prefixes
- Full OSS-Fuzz or unrelated backlog items

## Check

- Add failing test that servicesOver Refusals stays empty in `pkg/store/storetest/workspaces.go` Check: `go test ./pkg/store/storetest -run TestServicesOverRefusalsEmpty -count=1` fails on main
- Add failing test that OpenStateBlob errors omit the key in `pkg/backup/service.go` Check: `go test ./pkg/backup -run TestOpenStateBlobHidesKey -count=1` fails on main
- Add hostile List test for DeletePurgedBlobs and sweepUnreferenced in `pkg/workspace/service.go` Check: `go test ./pkg/workspace -run TestDeletePurgedBlobsRefusesForeignListKey -count=1` fails on main
- Add purge namespace ID test in NEW `pkg/store/storetest/blob_purge_namespace_test.go` Check: `go test ./pkg/store/storetest -run TestBlobPurgeKeepsNamespaceID -count=1` fails on main
- Edit the baseline of each dialect for blob_purges namespace_id in `pkg/store/sqlite/migrations` and `pkg/store/postgres/migrations`, regenerate sqlc Check: `make generate && git diff --exit-code` passes
- Refuse foreign list keys and share cleanupContext in `pkg/workspace/stateblobs.go` and `pkg/secret/itemkeyblob.go` Check: `go test ./pkg/workspace ./pkg/secret -count=1` passes
- Add empty-workspace guard in `pkg/workspace/service.go` Check: `go test ./pkg/workspace -run TestAuditOutOfScope -count=1` passes
- Replace tofu/ examples with namespaces/<namespace ID>/ in `pkg/blob/cache/cache.go` and `pkg/blob/blobtest/scope.go` Check: `rg -n 'tofu/' pkg/blob/cache pkg/blob/blobtest` returns no hits

Depends on:

- write-item-key-blobs-no
- `write-item-key-blobs-no` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- servicesOver returns workspace services over one shared blob store (`pkg/store/storetest/workspaces.go:4191 (func servicesOver)`)
- stateBlobs.refuse records out-of-scope refusals (`pkg/workspace/stateblobs.go:240 (func stateBlobs.refuse)`)
- secret.ItemKeyBlobs.refuse records out-of-scope refusals (`pkg/secret/itemkeyblob.go:346 (func ItemKeyBlobs.refuse)`)
- sweepUnreferenced deletes unreferenced blobs under a workspace prefix (`pkg/workspace/service.go:2462 (func Service.sweepUnreferenced)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 220: Isolation between namespaces on shared infrastructure (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (extended), 2 (extended), 3 (extended), 7 (current), 8 (current), 20 (current).
- Changed by: ADR 273 changes decision 12; ADR 286 changes; ADR 363 extends decisions 1, 2 and 3. Chain to the end: ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 273, 286, 363.
- Quote: "Pooled rows with a namespace ID. All namespaces share one database and one schema. Each row that belongs to a namespace holds its namespace ID. This includes the principals, groups, custom roles, grants, tokens, sessions, sign-"
- Quote: "A scoped view of the blob port. A domain service of a namespace gets a view of the blob store for that namespace. The view refuses each key outside its prefix with `blob.ErrOutOfScope`, before any call to the adapter. The `blob"
- Quote: "A blob scope test. A test calls each blob read of each domain service with a key of another namespace and checks `blob.ErrOutOfScope`."
- Effect: Change keeps pooled rows with namespace ID, keeps the namespace blob prefix and scoped view, and adds scope tests.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does storing the namespace ID on purge rows conflict with isolation rules? ADR 220 decisions 1, 2, 3, 7, 8 and 20 require namespace ID on rows, scoped view and scope tests; the change keeps them.
- Which existing helpers cover the failing-test-first work? servicesOver, Refusals, OpenStateBlob, cleanupContext and blob_purges exist on main; the change tightens their use.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
