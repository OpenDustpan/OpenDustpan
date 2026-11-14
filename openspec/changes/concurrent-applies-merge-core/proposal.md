Roadmap: `docs/roadmap.md:638` (Serve base per claim and merge atomically.).

## Why

**Persona: developer.** A developer changes one workspace with another team at the same time. The developer runs plan and apply through OpenDustpan runners and needs both disjoint applies to land with no lost resource.

The user path this change must make work:

1. **API.** Run tofu plan through a runner job and register a claim for the workspace
2. **CLI.** Run tofu apply from two jobs and read the base state of each claim
3. **API.** Upload new state; the server merges per instance and commits the next serial
4. **E2e.** Check the provider log overlap and the final state in an e2e run
5. **Docs page.** Read merge and strict-case behavior on the docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Base state version is superseded | Server answers 409 with code base-superseded and message Base state version is superseded. Replan from latest. Run `tofu plan`. | `tofu plan` |
| Upload changes an instance changed after the base | Server answers 409 with code instance-conflict and message Upload changes instance <addr> also changed after the base. Replan from latest. Run `tofu plan`. | `tofu plan` |
| Strict case holds whole-unit lock | Server answers 423 with code whole-unit-locked and message Workspace holds whole-unit lock for <case>. Run `dustpan workspace lock show <name>`. | `dustpan workspace lock show <name>` |

Evidence for the claims above:

- The TFE-compatible API serves state versions and uploads today. Proof: ANY /api/v2/state-versions and ANY /api/v2/state-uploads/{}/{} in the route table of the pack.
- The server checks lock, lineage, and serial again in one fenced transaction at finalize. Proof: ADR 26 decision 7 text in the pack: The finalize step checks the lock ID, the lineage and the serial again, in one SQL transaction, with the namespace fence (ADR 4).
- The pack names no merge test for concurrent applies. Proof: The keywords list of the pack holds only State version with no merge test name.

## What Changes

- Serve workspace state reads under a claim from the base state version named by the claim
- Merge state uploads one resource instance at a time under claim fence
- Commit each merged state version with the next serial in one transaction with compare-and-swap and crash safety
- Refuse overlapping writes, superseded bases, instance conflicts, empty uploads, hostile uploads, and mismatched fences fail-closed
- Hold the whole-unit lock for strict cases: data sources, providers configured from resources, root outputs, client-encrypted state, version or lineage mismatch, and local run without wrapper

Out of scope:

- Parent pointer and claim ID stored on each state version
- History display of the claim of each state version
- Audit entries for merges
- Sealed-value handling for merges
- Claim registration and overlap refusal, covered by two-disjoint-claims
- Guide for the wrapper and explanation of strict cases

Depends on:

- two-disjoint-claims

## Usability

Where the path still costs the person time or surprises them:

- A developer must learn base versus latest reads from the error text alone.
- A refused merge must name the blocking instance or base and the one command that fixes it.
- A strict-case wait must state the case and that the run holds the whole-unit lock.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "It needs a higher serial than the current state."
- Quote: "A force upload skips the lineage check and the serial check."
- Quote: "The finalize step checks the lock ID, the lineage and the serial again, in one SQL transaction, with the namespace fence (ADR 4)."
- Quote: "A retry of a finalized upload with the same capability and the same content succeeds."
- Effect: This change keeps lineage and fenced finalize checks and asks the RFC to change the serial rule to server assignment under a claim.
- Conflict: rfc_needed.

**ADR 281: Any S3-compatible store: the database decides which write wins** (owner-tagged (all decisions)).

- Decisions that apply: 11 (current).
- Changed by: ADR 340 changes decision 2. Chain to the end: ADR 340.
- Current rule: this ADR, with ADR 340.
- Quote: "Each write attempt runs with a fixed attempt deadline of 30 minutes."
- Quote: "The deadline covers the blob write and the fenced commit."
- Effect: Each merge attempt runs under the attempt deadline and a refused merge publishes no version, so no orphan version appears.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server serves each job the state version that the plan used as its base."
- Quote: "The server merges each upload one resource instance at a time."
- Quote: "Work that the server cannot prove disjoint takes turns."
- Quote: "The code of milestone 8 comes after the project owner accepts that RFC as an ADR."
- Effect: This change follows the job-aware merge rule and waits for the owner to accept the RFC for serial assignment, claim fence, and job principal.
- Conflict: rfc_needed.

## Open questions

1. How does a TFE-compatible request name its claim? Decides: owner.
   - Options: Job token credential names the claim on each TFE-compatible request; HTTP header names the claim alongside the existing lock credential.
   - Recommended: Job token credential names the claim on each TFE-compatible request. Reason: ADR 302 decision 6 leaves one principal for each job on the TFE-compatible path to the RFC, and the pack names no header or token for the claim.
2. What lease, renewal, and timeout govern a whole-unit wait under concurrent applies? Decides: owner.
   - Options: Reuse the existing workspace lock lease for whole-unit waits with no new duration; Set a separate bounded wait with an explicit timeout and a blocked status for the waiter.
   - Recommended: Reuse the existing workspace lock lease for whole-unit waits with no new duration. Reason: ADR 302 decision 6 leaves retry interval and timeout of the local wrapper to the RFC, and the pack states no duration for a strict-case wait.

Not spec-ready: a code check fails: no "main_facts": list what main already does (file:line and the function) from the pack; a code check fails: task "Add e2e step with real OpenTofu using a job token credential and a fak" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; the change conflicts with ADR 26: an RFC comes first; the change conflicts with ADR 302: an RFC comes first.

Questions considered and answered:

- What belongs in part 1 versus part 2 of the split? Part 1 serves the base named by the claim and merges uploads per instance under fence with atomic serial commit, refuse rules, empty uploads, fail-closed behavior, orphan safety, and strict whole-unit handling. Part 2 records parent pointers, history claim display, audit entries, and sealed-value handling.
- Which ADR decisions govern the merge? ADR 302 decision 2 requires per-instance merge and refusal of overlap. ADR 26 decisions 3 and 7 require lineage and fenced finalize in one transaction. ADR 281 decision 11 bounds each attempt to 30 minutes.
- What do reads without a claim return? Reads without a claim return latest. Reads under a claim return the named base. A pruned or deleted base fails closed with replan.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
