Issue: #1213.

## Why

**Persona: developer.** A developer pushes to a review request and wants a fresh plan for each stack and workspace the change touches without starting plans by hand.

The user path this change must make work:

1. **API.** Save a review-request-push trigger on the stack or workspace with its paths filter.
2. **E2e.** Push to the review request on the forge.
3. **Console.** Read the plan job, its head commit, and its log.
4. **Docs page.** Read the docs page for push plans and the fork rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A push from a fork starts nothing. | No plan starts and an audit entry records the fork push. Check the trigger target repository and the fork flag from the forge event. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| A push that touches no path of the filter starts nothing. | No plan starts. Check the paths of the trigger against the changed files of the push. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- Trigger already stores LastSaver and LastSaverID. Proof: pkg/trigger/trigger.go:190: LastSaver string and pkg/trigger/trigger.go:192: LastSaverID string
- No Dispatcher.ReviewRequest port exists yet. Proof: rg -n -F --glob '!*_test.go' 'Dispatcher.ReviewRequest' pkg internal cmd matches 0
- The source policy package exists at pkg/source. Proof: paths item path pkg/source exists true

## What Changes

- Trigger gains the Paths field with default to the working directory of the unit code location.
- Push intake matches units on repository and branch, filters on paths, refuses fork pushes with an audit entry, and replaces the older plan of the same trigger.
- Started jobs record source kind forge and run author code with unit profile credentials.

Out of scope:

- Schedule and follows triggers
- Pre-merge apply and apply after merge
- Full fork code policy in pkg/source beyond refuse and audit
- Plan comment and commit status on the review request

Depends on:

- run-triggers
- git-host-webhook-intake-post
- add-job-service-routes-start-1
- `run-triggers` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A push that starts nothing surprises the author; the trigger detail shows the matched repository, branch, and paths filter for that check.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 1 (current), 9 (current), 14 (changed), 15 (current), 19 (changed).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A trigger belongs to one unit and has one kind."
- Quote: "A trigger starts a plan and nothing else."
- Quote: "The start runs as the namespace service principal."
- Quote: "A push records `forge`."
- Quote: "A unit matches when the review request targets the repository and the branch of its code location."
- Effect: The change starts only review-request-push plans through the job service with source kind forge, repository and branch match, paths filter, start as the namespace service principal, and profile credentials by attachment authority.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only. The principal still has no token and no session. The rule of ADR 267 decision 14 about the read check of the last saver does not change."
- Effect: The push start runs as the namespace service principal with only the stored start-of-plan grant; the job gets unit profile credentials through the profile attachment authority and not through the principal token, and the last-saver read check does not change.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Check 2: the principal that started the job holds `unit:read-outputs` on the producer with its own current grants, and `unit:read-secrets` for a secret output."
- Effect: A push-started job reads producer outputs only when a sharing rule names it and the last saver of the trigger holds the read actions.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships the user docs page for push plans in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The ADR check omits ADR 299, which is owner-tagged and changes decisions 14 and 19 of ADR 267. It lists decision 19 as current and implemented. Per the ADR check rule this conflict blocks the change until the owner decides. Resolution: answered: Added ADR 299 entry with its Changed-by line; the change follows ADR 267 decisions 14 and 19 as changed by owner-tagged ADR 299 with no conflict.
- R1.2 (open question): The principal that starts a push-triggered plan is not named. ADR 338 decision 3 (owner) allows the namespace service principal only a start-of-plan grant, and the change says jobs run with unit profile credentials. The design must state which principal starts the job and which grants decide its reads. Resolution: answered: Design and specs now name the namespace service principal as starter with the stored start grant, attachment-authority credentials, and last-saver reads per ADR 338 and ADR 273.
- R1.3 (failure mode): Fork detection is undefined. The spec refuses fork pushes but does not say which field of the Forge port (#886) gives the fork flag, or what happens when the flag is missing or wrong. Resolution: answered: Fork flag source from the Forge port and refuse-when-missing with audit moved to considered and to the fork refusal requirements; no open question remains.
- R1.4 (failure mode): No requirement covers authentication of push deliveries. A spoofed or replayed forge event could start plans. The hostile-input layer and its test are missing. Resolution: answered: Added intake authentication requirement with spoof and replay scenarios and hostile intake layer and tasks; no open question remains.
- R1.5 (missing scenario): The paths match rule is undefined for pushes with many commits, renamed or deleted files, force pushes, and forge payloads that truncate the changed-file list. A truncated list must not silently start no plan or the wrong plan. Resolution: answered: Specs now define union over commits, rename as delete plus add, force-push on head files, and truncation handling with record on the job.
- R1.6 (failure mode): Replacing a running or queued plan is not specified. It does not say how a running plan is cancelled, what happens when the older plan holds a lock or is waiting for approval, or how the open-job limit of ADR 267 decision 20 applies to the replacement. Resolution: answered: Supersede requirement now defines cancel of queued plan, cancel request for running plan, lock and approval-wait handling, open-job limit, and cancel edge cases with serialization.
- R1.7 (missing scenario): Duplicate delivery of the same push, a push that deletes the branch, and a push whose head commit already has a plan are not covered. Each needs a stated result. Resolution: answered: Added duplicate delivery and branch-delete requirements with scenarios.
- R1.8 (missing scenario): The audit requirement covers only the refused fork push. Each automatic start must also record who or what started it and why, per the project rule. No scenario covers that record for a successful start. Resolution: answered: Added automatic start audit requirement and scenario for each successful start.
- R1.9 (scope): The usability section requires the trigger view to show the matched repository, branch and paths filter, but neither the spec nor tasks.md has a requirement or task for it. The definition of done also asks for routes with their authorization actions, and none are named. Resolution: answered: Added trigger-detail visibility requirement and named trigger routes with authorization actions in tasks.
- R1.10 (open question): Under ADR 273 decision 14 the last saver of the trigger decides output reads for a job it starts. The change stores LastSaver but does not say whether a push-started job uses it, which ADR 338 leaves unchanged. Resolution: answered: Starter requirement now states output reads use the last saver of the trigger per ADR 273 unchanged by ADR 338.
- R1.11 (missing scenario): Default paths for a code location that is a subdirectory of a repository, or the repository root, are not specified. The design defaults to the working directory without saying how the value is normalized. Resolution: answered: Default paths requirement now defines subdirectory, root marker, normalization, and trailing separator rule.
- R2.1 (wrong claim): The ADR check cites a quote for ADR 299 ('ADR 299 changes decisions 14 and 19: one credential rule for every start of') that is not a decision line. ADR 299 is owner-tagged and its decisions list is empty in the pack, so the owner-tagged conflict check has no decision text to test. The 'Conflict: none' claim has no proof and blocks the change until the owner decides. Resolution: answered: Removed the ADR 299 entry because the pack lists no decision text for ADR 299; the change follows ADR 267 with ADR 299 and ADR 338 in its Changed-by chain and claims no owner decision text.
- R2.2 (adr conflict): ADR 267 decision 14 applies to the starter of a push-started job and is marked changed, but the ADR check lists only decisions 1, 9, 15 and 19. The design and spec name the namespace service principal as starter, which relies on decision 14. The ADR check must quote decision 14 and its changes by ADR 273, 299 and 338. Resolution: answered: adr_check now lists ADR 267 decision 14 with its quote and follows its changes by ADR 273, 299 and 338.
- R2.3 (adr conflict): The proposal and spec say started jobs run author code with unit profile credentials, while ADR 338 decision 3 (owner) says the principal holds only the start-of-plan grant with no token and no session. The change does not say how the job obtains unit profile credentials for a principal that holds nothing else, so the credential path is unresolved against the owner decision. Resolution: answered: Design and specs now state that unit profile credentials come from the profile attachment authority and not from the starter token, with output reads by the last saver per ADR 273 and ADR 338 decision 3.
- R2.4 (open question): The fork flag source and the missing-flag result are open with PM as decider and a recommendation, but there is no 'Provisional decision (owner review 14 Nov)' label. The design already refuses on a missing flag, so the build cannot proceed without the answer. Resolution: answered: Fork flag source and missing-flag refusal moved to considered and to the fork refusal requirement; no open question remains.
- R2.5 (open question): Push delivery authentication (signature and delivery-ID deduplication) is open, yet the design lists intake authentication as a guard layer with a hostile test. The spec has no requirement or scenario for intake authentication, replay refusal or spoofed events, so a forged push could start a plan. Resolution: answered: Added intake authentication requirement with replay and spoof scenarios and hostile intake task; no open question remains.
- R2.6 (open question): The recommended answer for a truncated changed-file list is to match every trigger of the same repository and branch. That starts plans for stacks the push did not touch, and no ADR or owner decision supports it. It is not labeled as a provisional owner decision. Resolution: answered: Truncation handling moved to considered and to a requirement that records truncation on the job; no open question remains.
- R2.7 (missing scenario): The design's authorization-at-start layer (namespace fence, plan-only port, open-job limit, stored start grant checked at start) and its hostile test (cross-namespace unit, apply operation called directly at the service) have no spec scenario and no task. Fork refusal in depth (service guard and job-start guard) also has no scenario for each guard. Resolution: answered: Added authorization-at-start and per-guard fork refusal scenarios and tasks for each guard.
- R2.8 (failure mode): Two pushes to the same review request can arrive at once. Nothing requires the dispatcher to serialize trigger writes or supersede steps, as ADR 267 decision 17 does for trigger saves. Two plans could both proceed, and the newer head commit would not be the only plan left. Resolution: answered: Added serialization requirement and scenario so concurrent pushes leave only the newer head plan.
- R2.9 (missing scenario): The default paths requirement says 'the cleaned working directory with a trailing separator rule' without defining the rule. The root marker and subdirectory cases are not pinned, so the stored filter is not testable as written. Resolution: answered: Default paths requirement now defines the trailing separator rule, the root marker, and subdirectory cases.
- R2.10 (failure mode): The supersede requirement covers cancel of a queued plan and a cancel request for a running plan, but not a cancel that fails, a plan that finishes before the cancel lands, or a restart while the cancel is pending. Resolution: answered: Supersede requirement now covers failed cancel, finished-before-cancel, and restart-while-cancel-pending.
- R2.11 (missing scenario): The tasks list the e2e and definition-of-done steps but have no task for the fork-refusal guard tests with all other checks bypassed, nor for the hostile intake test. The mapping from scenarios to failing tests on main is incomplete. Resolution: answered: Tasks now map each scenario to a failing test on main, with fork-guard bypass tests and the hostile intake test.
- M.32 (dependency): The dependencies are issue IDs. This change is the single owner of the push start. Resolution: answered: Replaced issue-ID dependencies with change dependencies on run-triggers, git-host-webhook-intake-post, and add-job-service-routes-start-1; this change owns only the push start.

## Open questions

none

Questions considered and answered:

- How does a push match units and what does it start? ADR 267 decisions 1, 9, 14, 15 and 19 answer it as changed by ADR 273, 299, 338: one review-request-push trigger per unit, plan only through the job service, source kind forge, match on repository and branch with paths default, start as the namespace service principal.
- Which principal starts the job and which grants decide its reads? ADR 299 changes ADR 267 decisions 14 and 19 to one credential rule for every start; ADR 338 decision 3 and ADR 273 decision 5 answer it: the namespace service principal starts the plan with only the stored start grant, output reads use the last saver of the trigger, and unit profile credentials come from the profile attachment authority and not from the starter token.
- Which field gives the fork flag and what happens when the flag is missing? The Forge port gives the fork flag from head repository versus base repository; a missing flag fails closed with refusal and audit, which follows the fork refusal of the issue.
- How are push deliveries authenticated and deduplicated? The Forge port authenticates each delivery by signature with delivery-ID deduplication before the dispatcher runs; a failed check starts nothing.
- What does the dispatcher do when the forge payload truncates the changed-file list? A truncated changed-file list matches every trigger of the same repository and branch and records the truncation on the job, which fails toward a plan with a record instead of a silent miss.
- Does the change need new tooling? No new tool. The fix is a field and a matcher in the trigger domain with conformance cases, checked by make check and an e2e step against a fake forge.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
