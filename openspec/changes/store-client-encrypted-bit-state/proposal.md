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

- Add failing conformance tests for finalize storage and Latest row read on both dialects Check: workspace conformance test asserts finalize stores the bit and Latest returns it for plaintext and client-encrypted states on sqlite and postgres
- Add failing test that Latest with a present bit performs no blob read Check: blob-read counter test fails on main because Latest streams the blob
- Add failing test for legacy null-bit fallback Check: conformance test with a null-bit row fails on main expectations by asserting the streamed fallback path
- Add the client-encrypted column to each dialect baseline in name order Check: `make generate` plus `sqlc diff` clean with new column in each dialect baseline in name order and no new numbered migration file
- Write the streamed bit at finalize Check: `go test ./pkg/workspace/...` passes with finalize writing the bit in the exposing transaction
- Change Latest to read the stored bit without a blob read Check: `go test ./pkg/workspace/...` passes with Latest reading one row and falling back only on null
- Refuse a document with both encrypted_data and version with invalid state Check: `go test ./pkg/workspace/... -run TestBothKeys` passes
- Add guard test that a version cannot finalize without the bit Check: mutation test that removes the constructor rejection fails the conformance suite

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 9 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "Client-encrypted OpenTofu state. The server cannot read such a state (ADR 41 decision 8)."
- Quote: "The operation writes no resource rows for it."
- Effect: Decision 9 governs blame resource rows only and says the blame operation writes no resource rows for client-encrypted state. This change writes the bit to the state version row at finalize and writes no blame resource rows, then Latest takes the existing opaque path without a blob read.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The proposal says the bit goes in the baseline migration, but the issue asks for a new migration file per dialect. project.md forbids numbered migrations and allows only one baseline per dialect before the first tag; the spec does not say which rule applies. Ambiguous; the schema approach is not settled. Check file: proposal.md (Check and design.md Decisions 1). Resolution: answered: Column goes into each dialect baseline in name order per the project baseline rule; out_of_scope forbids numbered migrations and tasks check sqlc diff with no new migration file.
- R1.2 (missing scenario): No requirement covers the Latest answer for a state written before the change, which holds no bit. design.md says it must answer unknown, but the spec has no scenario for unknown and no rule for how the outputs route reports it. File: specs/workspace-state/spec.md. Resolution: answered: Added legacy-null-bit-fallback requirement with scenario legacy row streams once: a null-bit row streams the blob once and the outputs answer stays correct, with no unknown answer.
- R1.3 (open question): The proposal says old state versions are not backfilled, yet the design says old rows answer unknown. The spec does not say whether unknown makes a client-encrypted state read as plaintext, or whether a read falls back to the blob. The fallback rule is not given. File: design.md (Risks) and proposal.md (Open questions). Resolution: answered: Fallback rule in legacy-null-bit-fallback and no-write-back requirements: null streams the blob once, leaves the row null, never writes back.
- R1.4 (failure mode): The spec does not cover a finalize that fails after the blob is written but before the row holds the bit, or a crash between the two writes. The row could be stored without the bit, or the bit could disagree with the blob. File: design.md (Decisions 1) and specs/workspace-state/spec.md. Resolution: answered: Atomic-finalize requirement covers crash between blob write and row commit: version not visible until row commits, no row without a bit exposed.
- R1.5 (wrong claim): The ADR check quotes ADR 150 decision 9 as keeping opaque handling, and the proposal says Latest remains opaque. The quote is correct, but the ADR check does not mention that ADR 150 decision 9 says the operation writes no rows for such a state. The write-at-finalize design is not checked against it. File: proposal.md (ADR check). Resolution: answered: ADR check now quotes both sentences of decision 9 and states decision 9 governs blame rows only while this change writes the version row and no blame rows.
- R1.6 (missing scenario): The refuse-both-keys requirement covers only the upload failure. It does not say what a stored state with both keys does, or what the existing streamed meta does while it reads the first deciding key. The Out of scope section defers the early stop without a scenario. File: specs/workspace-state/spec.md. Resolution: answered: Added refuse-both-keys scenario both keys refused and legacy-null-bit-fallback scenario legacy document with both keys: full stream with no early stop refuses both-keys documents at upload and at fallback read.
- R1.7 (missing scenario): No scenario covers a Latest read when the state version is pending or when no state exists yet. The spec only covers the happy path of finalized versions. File: specs/workspace-state/spec.md. Resolution: answered: Added no-finalized-version requirement with scenarios no state yet and pending version only: no finalized version answers not found with no blob read.
- R1.8 (scope): Task 3.1 wires the row read into the tool-neutral and TFE outputs routes, but the spec and proposal do not list these routes as touched capabilities. The delta spec covers only workspace-state. File: tasks.md (3.1). Resolution: answered: Wiring task uses the existing States Latest port with no new routes; out_of_scope states outputs route responses are unchanged so the delta spec stays workspace-state.
- R1.9 (open question): The design names an unexported constructor newStateVersionRow that rejects an unset bit, but the mutation or guard test for the bit's absence is not in the tasks. A guard with a mutation test is required when no structure removes the need. File: design.md (Structure). Resolution: answered: Design names the single constructor and tasks add the mutation guard test that removes the rejection.
- R1.10 (wrong claim): The proposal Evidence cites pkg/workspace/service.go for abandonStream, which is not used by any listed claim. The claim that the upload computes the bit during finalize is not tied to a line number. File: proposal.md (Evidence). Resolution: answered: Evidence cites only readStateMeta, metaStream, and encrypted_data definitions with exact pack paths and no abandonStream reference.
- R2.1 (adr conflict): Proposal and design add the bit to each dialect baseline in place, but the issue asks for a new migration file per dialect, and project.md only allows one baseline per dialect before the first tag; the change says it resolves this but the spec and tasks do not record the rule, so the schema approach is still unsettled (proposal.md, Fix boundary and Check; design.md, Decisions 1). Resolution: answered: Design and tasks now record the baseline rule: column in each dialect baseline in name order, no numbered migration; numbered-migration check added and responses unchanged recorded in out_of_scope.
- R2.2 (wrong claim): The ADR check says ADR 150 decision 9 keeps Latest opaque and that the stored bit only selects the opaque path, but ADR 150 decision 9 covers blame rows and does not govern Latest reads; the claim that no conflict exists is not backed by a quoted Latest rule (proposal.md, ADR check, ADR 150 Effect). Resolution: answered: ADR check effect now states decision 9 governs blame rows only and does not govern Latest reads.
- R2.3 (failure mode): Atomic finalize is stated only as a requirement; the design does not say how the bit and the blob write are ordered or how a crash between blob write and row commit leaves a pending blob that a later finalize could expose with a stale or wrong bit. Resolution: answered: Design now orders blob write before the fenced row commit and states a crash leaves an unreferenced pending blob that Latest never reads.
- R2.4 (missing scenario): The refuse-both-keys scenario covers only upload; no scenario covers a stored document created before the change that holds both keys, or the behavior of the streamed fallback when the blob and the row bit disagree. Resolution: answered: Added scenarios for a stored pre-change document with both keys and for row-versus-blob precedence.
- R2.5 (open question): The legacy fallback streams the blob on every read of a null-bit row until a new finalize; the change does not say whether the fallback writes the bit back, which would change the no-backfill decision. Resolution: answered: Added no-write-back requirement: the null-bit fallback never writes the bit back.
- R2.6 (missing scenario): No scenario covers a read through the TFE outputs route when the stored bit is present but the blob is missing or unreadable, so the answer comes from the row with no blob check. Resolution: answered: Added scenario where the blob is missing but the stored bit is present and the row answers.
- R2.7 (scope): Task 3.1 asks for a row read in the outputs paths, but the delta spec and proposal name no route capability, and the Out of scope list does not say the outputs routes are unchanged in their responses. Resolution: answered: Out_of_scope now states the outputs route responses are unchanged; wiring task uses the existing States Latest port with no new routes.
- R2.8 (failure mode): Concurrent finalize of two versions in the same unit is not covered: the bit of the latest row must match the version exposed as latest under concurrent writers. Resolution: answered: Added concurrent-finalize scenario requiring the latest exposed row to carry the bit of the winning version.

## Open questions

none

Questions considered and answered:

- Does the change need to backfill old state versions? No. Rows finalized after the change carry the bit. Rows finalized before the change hold null and Latest falls back to a streamed read for those rows only, and the fallback never writes the bit back, so no backfill.
- Does Latest still treat client-encrypted state as opaque? Yes. The stored bit only selects the existing opaque path without a blob read and writes no blame resource rows.
- New migration file or baseline edit? Baseline edit. The project baseline rule allows one baseline per dialect until the first tag, so the column goes into each dialect baseline in name order. The issue text asks for a new migration file but the project rule wins, and tasks forbid a numbered migration.
- Does the stream stop at the first deciding key? No early stop. The stream reads the full document; the both-keys refusal is enforced at upload and proved by a test, per the issue constraint.
- What happens when the row bit and the blob disagree? When present, the stored row bit governs and no blob is read. When null, the streamed blob governs. A stored pre-change document with both keys is refused at read time through the fallback stream.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
