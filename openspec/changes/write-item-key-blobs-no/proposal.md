---
version: 1
---

Issue: #1266, #1948, #1977.

## Problem

Problem: main has ItemSealer, ItemKeyIndex and ItemKeyBlobs, but no production code writes an item key blob. ItemKeyIndex.ShredItemKey only sets shredded_at_ms, so a shred keeps the wrapped key. Comments in pkg/secret/itemsealer.go and pkg/job/recorder.go describe an orphan cleanup that does not exist.

## Evidence

- No production code builds the out-of-scope audit recorder Proof: rg -n -F --glob '!*_test.go' 'AuditOutOfScope' pkg internal cmd returned 0 matches.
- ItemKeyBlobs has no production caller Proof: rg -n -F --glob '!*_test.go' 'secret.ItemKeyBlobs' pkg internal cmd returned 3 matches: two in pkg/store/storetest/item_keys_backup.go and a comment in pkg/job/artifact.go.
- ShredItemKey has no production caller Proof: rg -n -F --glob '!*_test.go' 'ItemKeyIndex.ShredItemKey' pkg internal cmd returned 0 matches.

## Fix boundary

- Write and read each key blob through ItemKeyBlobs in pkg/secret/itemsealer.go, with the namespace ID of the resolved row and the namespace key version as the v<version> segment.
- Add the NEW constructor secret.NewAuditOutOfScope and pass its recorder to the sealer.
- Add the NEW method ItemKeySealer.Shred in pkg/secret/itemsealer.go. It calls ItemKeyIndex.ShredItemKey, then deletes the blob through ItemKeyBlobs.Delete.
- Correct the orphan cleanup comments in pkg/secret/itemsealer.go and pkg/job/recorder.go.

Out of scope:

- The running-backup guard: the delete runs at once after the commit
- The ledger record namespace ID
- The check of the other sweeps
- A route that shreds an item key
- A recurring job that retries failed blob deletes

## Check

- Write the failing shred test in pkg/store/storetest/item_keys_backup.go: after Shred the key blob is gone and an open fails with the shred error, on both stores. Check: TestItemKeyShredDeletesBlob
- Write the failing round-trip test in the NEW file pkg/secret/itemsealer_blobs_test.go with the namespace ID and the v<version> segment. Check: TestItemKeySealerRoundTripThroughBlobs
- Write the failing tests for a missing record, a seal after shred, a key of another namespace and an open without blob in the NEW file pkg/secret/itemsealer_shred_test.go. Check: TestItemKeyShredMissingRecord
- Write the failing test for a second Shred after a refused blob delete in the NEW file pkg/secret/itemsealer_retry_test.go. Check: TestItemKeyShredRetriesBlobDelete
- Write the sealer wiring, the recorder constructor and Shred in pkg/secret/itemsealer.go. Check: go test -race ./pkg/secret/...
- Correct the orphan cleanup comments in pkg/secret/itemsealer.go and pkg/job/recorder.go. Check: rg -n 'orphan cleanup' pkg/secret/itemsealer.go pkg/job/recorder.go shows no false claim
- Wire the sealer into pkg/job/recorder.go, which takes secret.ItemSealer. The e2e step is BLOCKED by a missing route that shreds an item key. Check: go test -race ./pkg/store/storetest/...
- Add the docs page on shred and key blob delete. The embedder completes the path from the page. Check: make docs-lint

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ItemKeyBlobs type exists (`pkg/secret/itemkeyblob.go:40 (func ItemKeyFamily)`)
- ItemKeyBlobs.Delete exists (`pkg/secret/itemkeyblob.go:216 (func ItemKeyBlobs.Delete)`)
- ItemKeyBlobs.Versions exists (`pkg/secret/itemkeyblob.go:233 (func ItemKeyBlobs.Versions)`)
- The Postgres adapter has the ShredItemKey query (`pkg/store/postgres/internal/db/queries.sql.go:22024 (func Queries.ShredItemKey)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Do LedgerRecord and TombstoneRecord store the namespace ID, the name, or both?
   - Options: Store the namespace ID and keep the name as a label; Keep the name only; Store both.
   - Provisional decision (owner review 14 Nov): Store the namespace ID and keep the name as a label
   - Reason: A name can be renamed, or removed and made again. The stable ID is the reference. This change does not depend on the answer. Benchmark: AWS CloudTrail records the immutable account ID next to names.
   - Persona impact: No persona is upset, because the gaps check uses the item key ID. Escape hatch: The name stays in the record as a label.
2. Which interim rule applies to a shred that runs during a backup?
   - Options: Delete the blob at once and accept that a running backup may copy a missing blob; Delay the delete until no backup runs.
   - Provisional decision (owner review 14 Nov): Delete the blob at once and accept that a running backup may copy a missing blob
   - Reason: A shred must remove the key without delay, and the later guard of the other deleters then covers it. Benchmark: PCI DSS requires prompt removal of cryptographic keys that are retired.
   - Persona impact: An embedder gets a clear rule and the backup check reports a missing blob. Escape hatch: The embedder runs the shred when no backup runs.

Questions considered and answered:

- Does the shred delete use a sweep runner pool? No. Owner decision, 13 Nov 2026 (queue P14) limits the sweep purpose to operator pools. The delete runs in the server process after the commit, so P14 does not apply.
- Does a retry of a failed delete need a job? No. A second Shred call deletes any remaining blob version.
- Does the gaps check need the namespace ID in ledger records? No. Issue 1266 says it matches by item key ID.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
