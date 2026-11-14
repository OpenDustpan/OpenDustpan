---
version: 1
---

Issue: #1433.

## Problem

Three small review follow-ups from the #1431 review: staged queries need one entry-point staging-name check, two copies of the job chunk rule can drift, and one item-key blob helper has no caller.

## Evidence

- checkStagingIdent guards staged checks in backup_item_keys.go Proof: pkg/store/postgres/backup_item_keys.go:51 (func checkStagingIdent) with calls at pkg/store/postgres/backup_item_keys.go:74, pkg/store/postgres/backup_item_keys.go:136, pkg/store/postgres/backup_item_keys.go:191, pkg/store/postgres/backup_item_keys.go:247
- checkStagedSealedState takes the staging name on the staged restore path Proof: pkg/store/postgres/backup.go:1323 (func checkStagedSealedState) and call at pkg/store/postgres/backup.go:1089
- Many staged queries build names with stagingTable Proof: pkg/store/postgres/backup_forge_links.go:40 and pkg/store/postgres/backup_notifications.go:60 use stagingTable(staging, name)
- Chunk validation exists in the domain and in the store Proof: pkg/store/internal/jobstore/record.go:772 (func CheckChunkRow) called at pkg/store/postgres/backup_item_keys.go:232 and pkg/store/sqlite/backup_item_keys.go:203, and pkg/job/record.go:392 (func ChunkReserve.valid)
- ItemKeyBlobs.Versions has no non-test qualified reference Proof: Command `rg -n -F --glob '!*_test.go' 'ItemKeyBlobs.Versions' pkg internal cmd` gives 0 matches; the method itself lives at pkg/secret/itemkeyblob.go:233 (func ItemKeyBlobs.Versions)
- No ADR in the pack covers this change Proof: Pack lists `adrs: []` and `rfcs: []`; no ADR decision covers staging names, chunk rules, or this helper

## Fix boundary

- One allowlist check of the staging schema name at the place where the restore import receives it, covering every staged query; per-check checkStagingIdent calls stay.
- One exported chunk-rule helper pkg/job.CheckChunkField called by both the domain valid path and jobstore.CheckChunkRow.
- Removal of ItemKeyBlobs.Versions, which has no production caller.

Out of scope:

- Behavior of restore, backup, job chunks, or shred; only the entry check, the shared rule, and the dead-code removal.
- Any change to #1431 beyond the three nits.
- Item-key rewrap and blob naming, owned by release-blocker-item-key-sealer; this change lands after it and only removes Versions when that change leaves no caller.

## Check

- Add the NEW CheckStagingSchema entry guard in pkg/store/postgres/backup_item_keys.go and call it before the staged restore check path at pkg/store/postgres/backup.go:1089, with a failing test for empty, leading-digit, and 64-char names and a passing 63-char name. Check: TestStagingNameEntryRejectsHostileSchema in pkg/store/postgres fails on main, passes after the fix.
- Add the NEW pkg/job.CheckChunkField helper for pkg/job/record.go:392 and pkg/store/internal/jobstore/record.go:772, with a failing test that domain and store agree on hostile chunk rows with unchanged errors on postgres and sqlite staged paths. Check: TestChunkRuleSharedByDomainAndStore in pkg/job fails on main, passes after the fix.
- After release-blocker-item-key-sealer lands, re-run the Versions caller search to confirm the rewrap adds no caller, then remove ItemKeyBlobs.Versions in pkg/secret/itemkeyblob.go:233 and update only its test callers. Check: `rg -n -F --glob '!*_test.go' 'ItemKeyBlobs.Versions' pkg internal cmd` shows no caller after release-blocker-item-key-sealer, then no definition or caller after removal.
- Add a scan test in pkg/store/postgres that lists stagingTable uses outside the guarded helpers. Check: TestStagingTableUsesStayGuarded in pkg/store/postgres fails when a new direct stagingTable use appears.
- Run the affected package unit tests with race, plus the new stagingTable-use scan test. Check: go test -race ./pkg/store/postgres/ ./pkg/job/ ./pkg/secret/
- Run the full check gate with zero findings. Confirmed by: CI (make check). Check: `make check`

Depends on:

- release-blocker-item-key-sealer
- `release-blocker-item-key-sealer` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Postgres backup staging checks the staging schema name with checkStagingIdent before item-key and job-artifact checks (`pkg/store/postgres/backup_item_keys.go:51 (func checkStagingIdent)`)
- Postgres backup staging builds staged table names with stagingTable (`pkg/store/postgres/backup.go:1316 (func stagingTable)`)
- Postgres backup staging checks staged sealed state with the staging name (`pkg/store/postgres/backup.go:1323 (func checkStagedSealedState)`)
- The domain chunk rule lives in the unexported reserve valid method (`pkg/job/record.go:392 (func ChunkReserve.valid)`)
- The store chunk check lives in jobstore.CheckChunkRow (`pkg/store/internal/jobstore/record.go:772 (func CheckChunkRow)`)
- The secret item-key blob type exposes a Versions method (`pkg/secret/itemkeyblob.go:233 (func ItemKeyBlobs.Versions)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- How does the entry check stay equal to checkStagingIdent? The NEW entry function CheckStagingSchema in pkg/store/postgres/backup_item_keys.go calls the existing checkStagingIdent at pkg/store/postgres/backup_item_keys.go:51 before any staged query, so the entry rule and the per-check rule are the same function and cannot differ.
- How to stop the two chunk-rule copies from drifting? Export one NEW helper pkg/job.CheckChunkField and call it from ChunkReserve.valid at pkg/job/record.go:392 and from jobstore.CheckChunkRow at pkg/store/internal/jobstore/record.go:772. Both callers keep their current sentinel errors and messages. Postgres and sqlite paths are covered because both call jobstore.CheckChunkRow at pkg/store/postgres/backup_item_keys.go:232 and in sqlite backup_item_keys.
- Keep or remove ItemKeyBlobs.Versions? Remove it now after release-blocker-item-key-sealer lands. The pack shows zero non-test qualified references with the exact command below, and the method lives at pkg/secret/itemkeyblob.go:233. The removal task re-runs the same rg after that change lands to show the rewrap does not call Versions. Re-add it in the shred change when that change needs it.
- Does the change touch an ADR? The pack names no ADR. The lists of ADRs and RFCs in the pack are empty, so no ADR decision covers staging names, chunk rules, or this helper. The new import edge is checked by existing lint gates in make check.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
