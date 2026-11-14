---
version: 1
---

Issue: #2187.

## Problem

A developer who reads ADR 37, 43 or 60 follows the old write-once blob-key rule because the backfill missed the ADR 281 links, so the docs point at a rule that no longer applies.

## Evidence

- ADR 26 decision 2 states the old write-once rule Proof: Pack ADR 26 decision 2 text: Blob keys. A blob key is immutable. Issue #2187 cites docs/adr/0026-opentofu-state.md:48. Pack confirms docs/adr/0026-opentofu-state.md exists.
- ADR 37 decision 2 states the one-time key rule Proof: Pack ADR 37 decision 2 text: The server writes each key one time (ADR 26). Issue #2187 cites 0037:68.
- The link rule needs both directions Proof: Issue #2187 rule section: ADR 296 decisions 6 and 8 (both directions of each change link).
- The pack excerpt for ADR 43 does not hold the journal write-once text Proof: Pack ADR 43 decisions list holds the corpus-test lines, for example: The corpus test passes, and the e2e run of that CLI with journaling on and off gives the same normalized export. The journal write-once key at 0043:234 comes from issue #2187 evidence.
- ADR 41 decision 11 restates the immutable-key rule Proof: Pack ADR 41 decision 11 text: A blob key is immutable (ADR 26), so the server seals an existing state blob into a new key and moves the pointer in a fenced transaction.
- ADR 281 decisions 11 and 12 cover the replacement rules Proof: Pack ADR 281 decision 11 text starts: The rule of the reaper (proposed by the author). Each family writes the blob before it commits the row. Pack ADR 281 decision 12 text starts: Audit checkpoint copies (proposed by the author). The copies keep a trust anchor that does not depend on the database.

## Fix boundary

- Add Changed-by ADR 281 items with verb Changes to ADRs 26, 37, 43 and 60
- Add matching Changes items to ADR 281 mapping decision 11 to ADRs 26, 37 and 43 and decision 12 to ADR 60
- Run make adr-index with no hand edit

Out of scope:

- Any behavior change to blob writes, journal keys, checkpoint copies, or restore
- Any rewording of ADR decision text
- Link edits for ADRs beyond 26, 37, 43, 60, 281 and 296 in this change, including a follow-up for ADR 41 decision 11

## Check

- Edit docs/adr/0026-opentofu-state.md to add the Changed-by ADR 281 line with verb Changes for decision 2, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes decision 2' docs/adr/0026-opentofu-state.md
- Edit docs/adr/0037-checkpoints-in-blobs-and-delta-uploads.md to add the Changed-by ADR 281 line with verb Changes for decision 2, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes decision 2' docs/adr/0037-checkpoints-in-blobs-and-delta-uploads.md
- Edit docs/adr/0043-pulumi-journaling-support.md to add the Changed-by ADR 281 line with verb Changes and no decision number, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes' docs/adr/0043-pulumi-journaling-support.md
- Edit docs/adr/0060-audit-log-port-and-log-sinks.md to add the Changed-by ADR 281 line with verb Changes for decision 18, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes decision 18' docs/adr/0060-audit-log-port-and-log-sinks.md
- Edit docs/adr/0281-any-s3-compatible-store-the-database-decides-which-write-wins.md to add Changes lines for decision 11 to ADRs 26, 37, 43 and decision 12 to ADR 60. Confirmed by: the driver's gates. Check: rg -n 'Changes:' docs/adr/0281-any-s3-compatible-store-the-database-decides-which-write-wins.md
- Run make adr-index in docs/adr/INDEX.md with no hand edit of the index. Confirmed by: the driver's gates. Check: make adr-index leaves docs/adr/INDEX.md with ADR 281 in the Current rule column of ADRs 26, 37, 43 and 60
- Run make docs-lint for the link self-test and form check. Confirmed by: the driver's gates. Check: make docs-lint passes with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- make adr-index regenerates the ADR index (`Makefile:183`)
- A write-once key test exists on main (`pkg/dustpan/writeonce_internal_test.go:20 (func TestWriteOnceKey)`)
- blobtest is the conformance suite every blob adapter must pass (`pkg/blob/blob.go:5`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 2 (changed).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2; ADR 368 changes decision 3. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 368, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220, 368.
- Quote: "Blob keys. A blob key is immutable. The key is `tofu/<workspace ID>/<state version ID>/<attempt>`. Each upload attempt writes a new key. The blob port gets this rule: a caller writes a key once. A `blobtest` case checks it. The key has no namespace segment, so an export for a whole namespace reads SQL."
- Effect: This change adds only a Changed-by line with verb Changes for decision 2 and a matching Changes line in ADR 281. No decision words change.
- Conflict: none.

**ADR 37: Pulumi checkpoints in blobs, and delta checkpoint uploads** (not owner-tagged).

- Decisions that apply: 2 (changed).
- Changed by: ADR 41 changes decision 8; ADR 43 changes decisions 4 and 8; ADR 43 extends decisions 2, 3 and 11; ADR 43 supersedes decision 9; ADR 45 changes decision 1; ADR 151 changes decision 1; ADR 220 changes decision 2. Chain to the end: ADR 41, ADR 43, ADR 45, ADR 151, ADR 220, ADR 60, ADR 103, ADR 146, ADR 238, ADR 240, ADR 352, ADR 62, ADR 154, ADR 210, ADR 217, ADR 273, ADR 286, ADR 363, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297.
- Current rule: this ADR, with ADR 41, 43, 45, 151, 220.
- Quote: "Blob keys. The server writes each key one time (ADR 26). The key is `pulumi/<stack ID>/<update ID>/<sequence number>`. Each save writes a new key. The key has no namespace segment, for the same reason as in ADR 26."
- Effect: This change adds only a Changed-by line with verb Changes for decision 2 and a matching Changes line in ADR 281. No decision words change.
- Conflict: none.

**ADR 43: Pulumi journaling support** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 151 changes decisions 6 and 10; ADR 220 changes decision 6. Chain to the end: ADR 151, ADR 220, ADR 154, ADR 210, ADR 217, ADR 273, ADR 286, ADR 363, ADR 238, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 151, 220.
- Quote: "The corpus test passes, and the e2e run of that CLI with journaling on and off gives the same normalized export."
- Effect: The pack excerpt does not hold the journal write-once text cited by issue #2187 at docs/adr/0043-pulumi-journaling-support.md:234 decision 6.1. This change therefore names ADR 43 with no decision number per ADR 296 decision 3 and adds only link lines. No decision words change.
- Conflict: none.

**ADR 60: The audit log is a port, and logs go to swappable sinks** (not owner-tagged).

- Decisions that apply: 18 (current).
- Changed by: ADR 63 extends decision 5; ADR 64 changes decision 17; ADR 64 extends decision 5; ADR 64 supersedes decision 19; ADR 65 changes decision 7; ADR 68 extends decision 21; ADR 72 changes decision 5; ADR 126 changes decision 3; ADR 126 extends decisions 4, 5, 8 and 9; ADR 151 extends decision 5; ADR 210 changes decision 8; ADR 240 changes decision 4; ADR 334 extends decision 12. Chain to the end: ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 151, ADR 210, ADR 240, ADR 334, ADR 124, ADR 216, ADR 73, ADR 75, ADR 103, ADR 166, ADR 249, ADR 280, ADR 298, ADR 129, ADR 154, ADR 217, ADR 220, ADR 247, ADR 286, ADR 297, ADR 354, ADR 128, ADR 273, ADR 322, ADR 110, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 63, 64, 65, 68, 72, 126, 151, 210, 240, 334.
- Quote: "Signed checkpoints are opt-in. The switch `--audit-checkpoints` (`DUSTPAN_AUDIT_CHECKPOINTS`) is off by default, and it needs `--audit-chain`, because a checkpoint signs the head of a chain. When it is on, the server wri"
- Effect: The pack excerpt for decision 18 holds only the truncated opt-in checkpoint text and does not show the immutable-key sentence. Issue #2187 cites the immutable-key sentence at docs/adr/0060-audit-log-port-and-log-sinks.md:223. This change adds only a Changed-by line with verb Changes for decision 18 and a matching Changes line in ADR 281. No decision words change.
- Conflict: none.

**ADR 281: Any S3-compatible store: the database decides which write wins** (owner-tagged (all decisions)).

- Decisions that apply: 11 (current), 12 (current).
- Changed by: ADR 340 changes decision 2. Chain to the end: ADR 340.
- Current rule: this ADR, with ADR 340.
- Quote: "The rule of the reaper (proposed by the author). Each family writes the blob before it commits the row. So the reaper must not delete a blob of an attempt that has not committed yet. - Each write attempt runs with a fixed attempt deadline of 30 minutes. The deadline covers the blob write and the fenced commit. A commit after the deadline fails, and the attempt does not count. - The reaper deletes a blob only when"
- Quote: "Audit checkpoint copies (proposed by the author). The copies keep a trust anchor that does not depend on the database, with these conditions: - A check finds the copies only through a listing of a fixed prefix for each scope and position (`.../<position>/<attempt>`), never through a database row. - The check verifies the signature of each copy. These are findings: a position with no copy, a copy with a bad signat"
- Effect: ADR 281 gets Changes lines: decision 11 to ADRs 26, 37 and 43 for one key per attempt with the database row deciding the winner, and decision 12 to ADR 60 for kept and checked checkpoint copies. No words of ADR 281 change.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How are the missing links added without editing decision words? ADR 296 decisions 6 and 8 allow only the Changed-by item plus the matching Changes item, then the generator writes INDEX.md. Decision 3 makes the decision number optional, used for ADR 43 where the pack lacks the 6.1 text. Verb Changes is used for all four because the old decisions apply only in part.
- Is docs/adr/INDEX.md edited by hand? No. The index file docs/adr/INDEX.md is generated by the make target adr-index at Makefile:183. A hand edit diverges from the generator.
- Why do ADR 280 and ADR 298 need no link in this change? Pack ADR 280 decisions hold scope, release gate, durable restore, 503 with Retry-After, topology, figures, blob-delete wait, and the 30-second lease. Pack ADR 298 decisions hold failover stay-up, 503 fail-fast, deadlines, pool leave, allowlist, effective settings, readiness, no half writes, unknown-commit writes, replication mode, leases across the gap, and the fault test. Neither decision list states a write-once blob-key rule that ADR 281 replaces, so no link is added here. The negative claim rests on these pack decision lists.
- Do ADR 41, ADR 45 or ADR 220 also restate the write-once rule? Pack ADR 41 decision 11 restates the immutable-key rule. It is left to a follow-up issue so this change stays at the four ADRs named in issue #2187. Pack ADR 45 and ADR 220 decision lists in the pack hold no write-once sentence, so no link is added for them here.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
