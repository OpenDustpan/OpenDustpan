---
version: 2
---

Issue: #1640.

## Problem

A developer reads the outputs of a large OpenTofu workspace and waits while the server streams tens of megabytes of state to learn one bit, twice on the tool-neutral route, for an answer of a few hundred bytes.

## Evidence

- The upload passes state bytes through metaStream Proof: pkg/workspace/state_meta.go defines metaStream and newMetaStream
- Latest state reads stream the blob through readStateMeta today Proof: pkg/workspace/state_meta.go defines readStateMeta; pkg/workspace/state_encrypted.go calls readStateMeta on content
- The client-encrypted envelope uses encrypted_data with strict shape rules Proof: pkg/workspace/tofu_state.go defines encryptedStateKey as encrypted_data and documents the envelope check

## Fix boundary

- Store the streamed client-encrypted bit as a column of the state version row at finalize
- Serve Latest from the stored column with one row read and no blob stream
- Refuse an upload with both encrypted_data and version as invalid state

Out of scope:

- Stop of the meta stream at the first deciding key without the both-keys refusal test
- Backfill of the bit for state versions finalized before the change, including write-back of the fallback result
- New numbered migration files; the column goes into each dialect baseline per the project rule
- Changes to the responses of the tool-neutral and TFE outputs routes; they keep their shapes and use the stored bit only

## Check

- Add failing conformance tests for finalize storage and Latest row read on both dialects Check: TestFinalizeStoresEncryptedBit sqlite and postgres fail on main
- Add failing test that Latest with a present bit performs no blob read Check: TestLatestWithPresentBitReadsNoBlob fails on main because Latest streams the blob
- Add failing test for legacy null-bit fallback Check: TestLegacyNullBitStreamsOnce fails on main expectations by asserting the streamed fallback path
- Add failing test for both-keys refusal with pending cleanup Check: TestBothKeysRefusedRemovesPending fails on main by asserting pending cleanup
- Add failing test for concurrent finalizes and finalize order Check: TestConcurrentFinalizeKeepsWinnerBit fails on main without the column
- Add failing test for shred and redaction precedence with present bit Check: TestShreddedLatestAnswersShreddedWithPresentBit fails without precedence check
- Add the client-encrypted column to each dialect baseline in name order Check: `make generate` plus `sqlc diff` clean with new column in each dialect baseline in name order and no new numbered migration file
- Write the streamed bit at finalize Check: `go test ./pkg/workspace/...` passes with finalize writing the bit in the exposing transaction

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- readStateMeta reads the top-level members of the state stream to learn the client-encrypted bit (`pkg/workspace/state_meta.go:42 (func readStateMeta)`)
- metaStream reads the meta of one state while its bytes pass through it (`pkg/workspace/state_meta.go:304`)
- Encrypted envelope key is encrypted_data with strict shape rules (`pkg/workspace/tofu_state.go:27`)
- The declare step of a state version creates its item key path and pending version (`pkg/workspace/state_meta.go:308 (type metaStream)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "Client-encrypted OpenTofu state. The server cannot read such a state (ADR 41 decision 8)."
- Quote: "The operation writes no resource rows for it."
- Effect: Decision 7 orders state versions by finalize and covers forced writes and restores as state items like any other, so every finalize path stores the bit. Decision 9 governs blame resource rows only and says the blame operation writes no resource rows for client-encrypted state. This change writes the bit to the state version row at finalize and writes no blame resource rows, then Latest takes the existing opaque path without a blob read.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the change need to backfill old state versions? No. Rows finalized after the change carry the bit. Rows finalized before the change hold null and Latest falls back to a streamed read for those rows only, and the fallback never writes the bit back, so no backfill.
- Does Latest still treat client-encrypted state as opaque? Yes. The stored bit only selects the existing opaque path without a blob read and writes no blame resource rows.
- New migration file or baseline edit? Baseline edit. The project baseline rule allows one baseline per dialect until the first tag, so the column goes into each dialect baseline in name order. The issue text asks for a new migration file but the project rule wins, and tasks forbid a numbered migration.
- Does the stream stop at the first deciding key? No early stop. The stream reads the full document; the both-keys refusal is enforced at upload and proved by a test, per the issue constraint.
- What happens when the row bit and the blob disagree? When present, the stored row bit governs the encrypted check and no blob is read. When null, the streamed blob governs. A stored pre-change document with both keys is refused at read time through the fallback stream.
- Does the stored bit bypass shred or redaction? No. A shredded or redacted item answers shredded or redacted before any outputs answer, and the stored bit never bypasses that check.
- Is the cost that legacy rows keep until their next finalize an open question? No owner decision is needed. Legacy rows stay correct through the null fallback. They pay the streamed cost until their unit finalizes a new version. This change adds no backfill and no write-back, and the design risks and the out_of_scope list record this.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
