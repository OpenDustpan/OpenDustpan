Issue: #1642.

## Why

**Persona: developer.** Developer reads the outputs of a producer stack from a consumer job while the producer runs a journaled update, and needs one predictable answer for what latest state means instead of a read that sometimes replays an unfinished update.

The user path this change must make work:

1. **API.** Consumer unit reads the outputs of a producer stack through the tool-neutral outputs read while a journaled update of the producer runs.
2. **Docs page.** Reader follows docs/guides/output-sharing.md for the latest-state rule during a running update.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Consumer unit has no sharing rule from the producer | 403 with error code output-sharing.denied names the missing sharing rule and the command that lists the units of the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Producer stack has no completed checkpoint yet | 404 with error code unit.unknown-state names the completed checkpoint that is missing and the command that lists the units of the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- The output sharing guide exists at the path the change updates. Proof: docs/guides/output-sharing.md
- The pack names no ADR for this item, so no ADR quote applies. Proof: Context pack adrs list is empty and the issue text cites no ADR.

## What Changes

- Output reads during a journaled Pulumi update return the outputs of the last completed checkpoint and name its state version id.
- docs/guides/output-sharing.md states that the running journal is not part of latest state for consumer reads.

Out of scope:

- Rebuilding a deployment from journal batches on output reads
- Changes to the export of a stack by its owner
- New output sharing rules or settings

Depends on:

- `output-sharing` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A consumer may read outputs that are older than the running update; the answer names the completed state version id so the staleness is visible.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md states the spec-ready gate is not met: two code checks fail on routes (sharing-rules and units/{ref}/state) that are not in the API description or route table. The gate forbids implementation until Open questions reads none. Resolution: open question: Error help uses only the list-units route from the coverage note; the latest-state choice stays as an owner open question so the gate is not met.
- R1.2 (adr conflict): The ADR check says No ADR is touched and the decisions list is empty, but the change decides what latest state means for output reads, a stable-ID and consistency rule the ADR check was required to evaluate. The check gives no quoted decision lines and no evidence for the 'no ADR' claim (rule 5 requires proof). Resolution: answered: adr_check stays empty with evidence that the pack adrs list is empty and the issue cites no ADR; the latest-state choice is an owner open question.
- R1.3 (missing scenario): No requirement covers a producer stack with no completed checkpoint yet, although the proposal's error table lists a 404 unit.unknown-state answer. The spec has no scenario for that refusal. Resolution: answered: Added scenario 'Read before first checkpoint' to requirement 'Refuse read with no completed checkpoint' to cover the 404 unknown-state case.
- R1.4 (missing scenario): No requirement covers a consumer without a sharing rule (403 output-sharing.denied), although the error table lists it. The authorization of the read is not specified. Resolution: answered: Added scenario 'Read without rule' to requirement 'Refuse read without sharing rule' to cover the 403 denied case.
- R1.5 (failure mode): Concurrency and restart are not covered: a checkpoint can complete between the read of the running-update flag and the load of the checkpoint, and a restart during an update may leave the last completed checkpoint ambiguous. The design's constructor refusing a running update id is not specified in the spec or the tasks. Resolution: answered: Requirement Stable read during completion race and the constructor rule in design and tasks cover the race and restart.
- R1.6 (open question): The issue asks to decide whether latest state includes the running journal; the design decides no, but the decision is made silently without an ADR or RFC and the owner did not decide it. The change must record this as an open question that names the decider, or cite a decision that answers it. Resolution: open question: Latest-state choice is kept as an owner open question with options and a recommended bounded read.
- R1.7 (wrong claim): The proposal says the export of a stack (unit:read) and the output read must agree, and that export 'uses the last completed checkpoint while an update runs'. The issue states the export builds the checkpoint of a running update from its stored batches, which contradicts this claim; the change does not say whether unit:read changes. Resolution: answered: Design and considered state the owner export keeps its journal rebuild and only consumer reads are pinned.
- R1.8 (scope): Tasks 2.1 names the route /units/{ref}/outputs with unit:read-outputs authorization, but the spec and proposal never define this action or route; the authorization action is not declared as a stored grant. Resolution: answered: Tasks name no invented outputs route or action; wiring refers to the output read path only.
- R1.9 (missing scenario): The spec does not cover a read that races with the completion of the running update, or a read of a stack whose update was cancelled or failed mid-journal; the last completed checkpoint for those cases is not defined. Resolution: answered: Added scenarios 'Checkpoint completes during read' and 'Update cancelled or failed mid-journal' to requirement 'Stable read during completion race' to cover the race and cancel cases.
- R1.10 (failure mode): The Risks section accepts stale outputs but no requirement bounds the staleness or tells the consumer that outputs are older; the usability note relies on the version id alone, with no error or warning field defined in the API. Resolution: answered: Requirement Name the source checkpoint returns the serving version id with a flag that an update runs.
- R2.1 (open question): The change pins consumer reads to the last completed checkpoint, but the latest-state decision is still listed as an open question owned by the owner; the spec-ready gate requires Open questions to read none, so implementation must not start until the owner decides. Resolution: open question: Kept the latest-state decision as an open question for the owner, so the change states implementation must wait.
- R2.2 (wrong claim): The ADR check states that the change touches no ADR, yet the decision it makes (what latest state means for output reads) is a stable-ID and consistency decision; the 'no ADR' claim gives no quoted lines beyond an empty pack list, so the 'No ADR conflict' claim is unproven. Resolution: open question: Kept adr_check empty with evidence that the pack adrs list is empty, and kept the latest-state choice as an owner open question so no ADR conflict is claimed as proven.
- R2.3 (failure mode): The design's constructor that refuses a running update id is not specified in the spec or tasks as a requirement with a failing test for a restart during an update; a restart may leave the last completed checkpoint ambiguous and no scenario covers it. Resolution: answered: Added a restart mid-journal scenario to the stable-read requirement and a failing-test task that covers restart and constructor refusal.
- R2.4 (missing scenario): The spec has no scenario for a consumer whose sharing rule is removed while a read is in flight, or for a producer stack that is deleted or renamed; the stable stack ID rule is not applied to the producer reference. Resolution: answered: Added scenarios for a rule removed in flight and for a deleted producer by stable ID to the refusal requirements.
- R2.5 (failure mode): The 'Name the source checkpoint' requirement defines a running flag in the answer, but no requirement defines the shape of that field or its error code, so clients cannot rely on it for the visible staleness the usability note promises. Resolution: answered: Fixed the source-checkpoint requirement text to state the version id and the running flag in one sentence.
- R2.6 (scope): Task 2.2 requires a runbook command in docs/guides/output-sharing.md to run as written, but the proposal's docs step and the tasks do not name the command or a test that runs it. Resolution: answered: Named the docs check command in the tasks as the list-units API call that the runbook uses.
- R2.7 (open question): The proposal's error table gives the help command for the 404 as the deleted-units route, which does not list completed checkpoints; the answer that names the missing completed checkpoint has no valid helping command. Resolution: answered: Changed the 404 help command to the list-units route from the coverage note.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does latest state for consumer output reads exclude the batches of a running journaled Pulumi update?
   - Options: Pin consumer output reads to the last completed checkpoint during a running journaled update; Include running journal batches in latest state with a bounded replay.
   - Provisional decision (owner review 14 Nov): Pin consumer output reads to the last completed checkpoint during a running journaled update.
   - Reason: After a step back, reads must stay bounded and stable, and the issue asks for a bounded read from the update row without a deployment rebuild. Benchmark: AWS versioned reads serve one immutable completed version and name its version id so readers see staleness.
   - Persona impact: Pinning keeps one predictable answer for a Developer and bounds read cost; the answer names the serving version id so staleness is visible. Escape hatch: Consumers that need fresher outputs wait for the update to complete and read again with the new version id.

Questions considered and answered:

- Must an output read replay the journal batches of a running update? No. The issue asks for a bounded read. Rebuilding the deployment from batches on each output read adds unbounded cost and a second definition of latest state. Reads stay on the last completed checkpoint.
- Can the owner export and a consumer read see different latest states during an update? No. The owner export keeps its journal rebuild behavior and is out of scope. Only consumer output reads are pinned to the last completed checkpoint, and the guide states the difference.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
