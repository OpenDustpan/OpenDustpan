Issue: #1211.

## Why

**Persona: developer.** A Developer keeps unit B in sync with unit A. The Developer sets a follows trigger on B, applies A, and expects one plan of B per applied version, with no extra runs and no silent skip.

The user path this change must make work:

1. **API.** Create a follows trigger on unit B with source unit A and when applied through the API.
2. **CLI.** Apply unit A with the real OpenTofu CLI from a workstation or through a job.
3. **API.** Watch the plan job of unit B start once for the new final version and read the live log.
4. **Docs page.** Read the docs page that names which runs count as an apply and the saver read rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Event for a version older than the last final version of the unit. | The server answers 409 with code trigger.version-mismatch and starts nothing. Send the newer final version. Run `opendustpan status` to see the latest final version of the unit. | `opendustpan status` |
| Job service refuses the plan for one follower. | The server records an error on that trigger and continues with the other followers. Fix the unit settings or pool, then fire the trigger by hand. Run `opendustpan status` to see the trigger error and the version to refire. | `opendustpan status` |
| Follows plan reads a source output without permission. | The server denies the read and fails the step that reads. Ask a namespace administrator to change the grants of the last saver. Run `opendustpan audit-chain` to see the denial entry. | `opendustpan audit-chain` |

Evidence for the claims above:

- The trigger dispatcher port and its conformance harness already exist. Proof: pkg/trigger/ports.go:136:// Outcome is the result of one trigger in a Dispatcher.Upstream call. and pkg/trigger/triggertest/dispatcher.go:76:out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: applied})
- The version-mismatch sentinel already exists in the trigger domain. Proof: pkg/trigger/trigger.go:45:// ErrVersionMismatch reports an upstream event whose state version is not and pkg/trigger/dispatcher.go:129:return nil, ErrVersionMismatch
- Trigger rows already carry the last saver subject and stable principal ID. Proof: pkg/trigger/trigger.go:170:// LastSaver is the subject of the principal that saved the trigger last. The and pkg/trigger/trigger.go:190:LastSaver string

## What Changes

- Dispatcher.Upstream verifies the final state version, enforces one plan per trigger and version, returns ErrVersionMismatch for an older version, and records per-trigger start errors.
- OpenTofu final-version write sends the upstream event; Pulumi sends nothing in this slice.
- Follows plan jobs carry the trigger ID and check each source output read against the current grants of the last saver.
- Follows triggers publish a declared dependency edge for the graph and safe delete.
- Manual fire of a final version through the trigger fire route refires missed followers with the same idempotency key.
- Docs page names which runs count as an apply.

Out of scope:

- Follower caps at run time and depth guard beyond the save-time cycle and follower-count checks
- The when value outputs-changed and its changed-outputs filter
- Pulumi update sender; only the OpenTofu final-version sender ships here
- Schedule and review-request-push triggers

Depends on:

- write-state-version-unit-state-1
- job-service-unit-settings-pool
- output-sharing
- `write-state-version-unit-state-1` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A Developer who expects a follower plan after every run is surprised when a plan with no state change starts nothing; the docs page states that only a final state version counts.
- A refire after a missed event needs the version ID from unit history; the error names the history command path.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current), 11 (current), 12 (current), 13 (current), 14 (changed), 15 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "**A `follows` trigger names a source unit, and the server refuses a cycle at save.** After the source applies, a plan of the unit starts. The source is a unit reference, so a unit of one tool can follow a unit of another"
- Quote: "A `follows` trigger is a declared dependency edge. ADR 153 decision 2 says that a feature that declares a link between units adds its own kind of edge, with the evidence `declared`. The `follows` trigger does that. Such"
- Quote: ""A applies" uses only what the server can see (question 1). - For the Pulumi CLI, an update of the kind `update` counts. A refresh, a preview and a destroy do not. - For OpenTofu over the state backend, any final state v"
- Quote: "A `follows` trigger has a field `when` (question 2). The default is `applied`: after each apply of the source. The value `outputs-changed` comes in a later slice."
- Quote: "The principal of a start that no person makes (question 3). - The start runs as the namespace service principal. That principal holds only the action that starts a plan. - For a job that a trigger starts, check 2 of ADR"
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind. The port that a trigger uses to start a job can start a plan and nothing else. No trigger starts an apply in these slices (RFC"
- Quote: "The source kinds (question 4). A `schedule` trigger records the source kind `schedule`. A push records `forge`. A `follows` start records the new source kind `trigger`. A manual fire keeps the source kind of the caller ("
- Effect: This change implements the applied start, the declared edge, the sender filter, the plan-only start, the saver read rule and the trigger source kind.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).** A job token reads the outputs of another unit only when both checks pass at each read: - Check 1: a sharing rule of the producer names the unit of the job, its collection or its namespace. - Check 2: the principal that started the job holds `unit:read-outputs` on the producer with its own current grants, and `unit:read-secrets` for a secret output.  The job gets the intersection of the two checks, never more. Check 2 uses `unit:read-outputs` in place of `unit:read`. This supersedes that part of ADR 213 decision 1.  The principal whose grants decide check 2 is one of these: - The principal that started the job (ADR 213 decision 1). - The last saver of the trigger that started the job (ADR 267 decision 14). - For an open of the profile block `outputs/opendustpan` by a job, the attachment authority (ADR 207 decision 7): the principal that made the attachment, or the blueprint publisher of a Catalog attachment. The grants of the starter do not count.  No caller gives the `Checker` a principal whose grants decide. For an open of the profile block, the profile service passes only a reference (`ReadReq"
- Effect: The read of source outputs by a follows plan uses the last saver check with unit:read-outputs and unit:read-secrets.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only. The principal still has no token and no session. The rule of ADR 267 decision 14 about the read check of the last saver does not change."
- Effect: The plan starts as the namespace service principal with only the stored plan-start grant; output reads still use the last saver.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The change ships the docs page that names which runs of each tool count as an apply in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The user path and the error table name commands that the code check reports as missing (opendustpan job and opendustpan grant). The one command that helps does not exist, so the usability requirement cannot be met as written. Resolution: answered: Replaced the missing job and grant commands with existing commands opendustpan status and opendustpan audit-chain; the trigger fire path is now a REST manual fire plus docs, covered by a new refire requirement.
- R1.2 (open question): The Open questions section does not read none. It records failed code checks for the job and grant commands, so the spec-ready gate refuses the change. Resolution: answered: Open questions reads none; the command errors are fixed and no new undecided question remains.
- R1.3 (missing scenario): The design says the refire path covers a missed event, and the error table tells the person to run trigger fire with a version ID. The spec has no requirement or scenario for a manual refire, for a refire of an already fired version, or for the 409 trigger.version-mismatch answer. Resolution: answered: Added manual refire requirement with 409 trigger.version-mismatch scenarios and the idempotency replay scenario.
- R1.4 (failure mode): The design says Upstream writes the cursor in the same transaction as the job start. The job start goes through the job service (ADR 267 decision 2), which may use its own store. A crash between the job start and the cursor write is not covered by any scenario, and the replay path is not specified. Resolution: answered: Added crash-replay scenario: a retry after a start uses the same per-trigger idempotency key and starts nothing new; cursor write rides the job-start transaction.
- R1.5 (adr conflict): ADR 338 decision 3 (owner, 2026-11-07) says the plan starts as the namespace service principal with only the stored plan-start grant. Neither the spec nor the design states the starting principal or checks this grant on start. Resolution: answered: Added starting-principal requirement that the plan starts as the namespace service principal under its stored plan-start grant.
- R1.6 (wrong claim): The ADR check says ADR 267 decision 14 is a conflict none, but the decision list omits decision 14 and its quote. Decision 14 is changed by ADR 273, 299 and 338 and governs the saver read check that the spec relies on. Resolution: answered: Added ADR 267 decision 14 to the ADR check with its exact decision line.
- R1.7 (missing scenario): The issue requires that the version ID has only the bytes a-z, A-Z, 0-9, dash, underscore and dot. The spec has no scenario for a malformed or hostile version ID in an event or in the refire command. Resolution: answered: Added version-ID charset requirement with hostile version-ID scenarios.
- R1.8 (missing scenario): The tasks name routes with the unit:manage-triggers action, and the project requires each endpoint to declare its authorization actions. The spec defines no route, no action and no denial for a caller without that action. Resolution: answered: Added trigger-route authorization requirement for unit:manage-triggers with denial scenario.
- R1.9 (missing scenario): ADR 267 decision 7 says the server refuses a cycle at save. The proposal puts only the depth guard and cycle path beyond save-time refusal out of scope, but no requirement states the save-time refusal itself. Resolution: answered: Added save-time cycle refusal requirement per ADR 267 decision 7; run-time depth stop stays out of scope.
- R1.10 (failure mode): Source follower caps are out of scope, but one Upstream call with no cap can start many plans at once. ADR 267 decision 20 sets a limit of 50 followers per source. The change does not say what happens above the limit or whether the fan-out is bounded. Resolution: answered: Clarified that the fan-out is bounded by the save-time limit of 50 followers per source; run-time caps stay out of scope.
- R1.11 (scope): The spec says safe delete of the source names the follower. The issue says a forced delete is the only way past a follower edge. The spec does not state that a normal delete is refused while a follower edge exists. Resolution: answered: Added delete rule: normal delete of a source with followers is refused and only a forced delete passes the follower edge.

## Open questions

none

Questions considered and answered:

- What counts as an apply of the source unit? ADR 267 decision 12 answers it: for OpenTofu any final state version counts; for the Pulumi CLI only an update of kind update counts. The docs page states this rule.
- When does a follows trigger fire? ADR 267 decision 13 answers it: field when with default applied; outputs-changed is the later slice write-state-version-unit-state-outputs-changed-filter and is out of scope here.
- Which principal starts the plan and whose grants govern source output reads? ADR 267 decision 15 plus ADR 273 decision 5 answer it: record source kind trigger, start as namespace service principal per ADR 338, and check output reads against the current grants of the last saver with unit:read-outputs plus unit:read-secrets for secret outputs, fail closed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
