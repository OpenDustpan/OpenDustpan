---
version: 3
---

Issue: #1640.

## Problem

A server read of the latest OpenTofu state version streams tens of megabytes of state to learn one bit. The cost repeats on each outputs read, for an answer of a few hundred bytes.

## Evidence

- The upload passes state bytes through metaStream Proof: pkg/workspace/state_meta.go defines metaStream and newMetaStream
- Latest state reads stream the blob through readStateMeta today Proof: pkg/workspace/state_meta.go defines readStateMeta; pkg/workspace/state_encrypted.go calls readStateMeta on content
- The client-encrypted envelope uses encrypted_data with strict shape rules Proof: pkg/workspace/tofu_state.go defines encryptedStateKey as encrypted_data and documents the envelope check

## Fix boundary

- Store the streamed client-encrypted bit as a column of the state version row at finalize
- Serve the Latest port from the stored column with one row read and no blob stream
- Keep the refusal of a document with both encrypted_data and version

Out of scope:

- The API field that shows the bit and the e2e step: they move to the change store-client-encrypted-bit-state-api-e2e, stacked after this change
- Stop of the meta stream at the first deciding key
- Backfill or write-back of the bit for state versions finalized before the change
- New numbered migration files; the column goes into each dialect baseline
- Changes to the responses of the outputs routes

## Check

- Record each difference between this spec and main in the implementer's notes (the pull request body), with repository paths such as pkg/workspace/... Check: The pull request body lists each difference
- Add failing conformance tests for finalize storage on both dialects Check: TestFinalizeStoresEncryptedBit fails on main
- Add a failing test that Latest with a present bit reads no blob Check: TestLatestWithPresentBitReadsNoBlob fails on main
- Add a failing test for the legacy null-bit fallback Check: TestLegacyNullBitStreamsOnce fails on main
- Add failing tests for both-keys refusal, concurrent finalize and shred precedence Check: TestBothKeysRefusedRemovesPending, TestConcurrentFinalizeKeepsWinnerBit and TestShreddedLatestAnswersShreddedWithPresentBit fail on main
- Add the column to each dialect baseline in name order Check: `make generate` and `sqlc diff` are clean with no numbered migration file
- Write the bit at finalize and read it in Latest Check: `go test ./pkg/workspace/...` passes
- Add a guard test that removes the constructor rejection Check: The mutation fails the conformance suite

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- readStateMeta reads the top-level members of the state stream (`pkg/workspace/state_meta.go:42`)
- metaStream reads the meta of one state while its bytes pass through it (`pkg/workspace/state_meta.go:308`)
- The envelope key is encrypted_data (`pkg/workspace/tofu_state.go:27`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "Client-encrypted OpenTofu state. The server cannot read such a state (ADR 41 decision 8)."
- Quote: "The operation writes no resource rows for it."
- Effect: Decision 7 orders state versions by finalize, so every finalize path stores the bit. Decision 9 governs blame rows only. This change writes the bit to the version row and writes no blame rows.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the change need a backfill? No. Rows with a null bit use the streamed read, and the read never writes the bit back.
- New migration file or baseline edit? Baseline edit in each dialect, in name order. The project rule allows one baseline per dialect until the first tag.
- Does the stream stop at the first deciding key? No. The full document is read and the both-keys refusal stays at upload.
- Does the stored bit bypass shred or redaction? No. A shredded or redacted item answers before any outputs answer.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
