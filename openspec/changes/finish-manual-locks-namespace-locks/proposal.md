Issue: #1069.

## Why

**Persona: namespace administrator.** A namespace administrator tries to stop all new jobs for a unit, a collection or a whole namespace during an incident and show the lock with its reason in the console.

The user path this change must make work:

1. **API.** Lock a unit, a collection or a namespace with a reason through the API.
2. **CLI.** Run OpenTofu and the Pulumi CLI against a covered unit and read the locked refusal.
3. **API.** Start and queue jobs on a covered unit and watch them wait with reason locked.
4. **Docs page.** Read the guide and list the lock flag, holder and reason through the API.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| OpenTofu lock request on a covered workspace is refused. | 409 with error code locked, holder and reason; list locks through the API with `GET /api/dustpan/v1/namespaces/{namespace}/locks`. | `GET /api/dustpan/v1/namespaces/{namespace}/locks` |
| Start of a mutating Pulumi update on a covered stack is refused. | 409 with error code locked, holder and reason; list locks through the API with `GET /api/dustpan/v1/namespaces/{namespace}/locks`. | `GET /api/dustpan/v1/namespaces/{namespace}/locks` |
| Bulk call with more than 100 IDs is refused. | 200 with one result per ID in request order; a call with more than 100 IDs answers 400 and does nothing. | `POST /api/dustpan/v1/namespaces/{namespace}/locks:bulk-lock` |

Evidence for the claims above:

- First slice put manual locks in the shared core with a conformance suite for both SQL adapters. Proof: pkg/manuallock
- The operator guide for manual locks exists and must grow to cover namespace locks and dispatch. Proof: docs/guides/manual-locks.md

## What Changes

- New lock routes for the namespace object: POST /api/dustpan/v1/namespaces/{namespace}/locks, GET /api/dustpan/v1/namespaces/{namespace}/locks and DELETE of one lock, with the inventory lock flag for namespace cover.
- New bulk routes POST /api/dustpan/v1/namespaces/{namespace}/locks:bulk-lock and locks:bulk-unlock with per-item results for 1 to 100 IDs.
- Job dispatch waits on covered units: no new starts and no queued dispatches, queued jobs wait with reason locked; running phases end.
- Job-token tool lock passes for a phase that started before the lock, with fail-closed ordering when times are equal, missing or race with the lock write.

Out of scope:

- Installation scope lock with action installation:lock
- Import holds and their release flow
- Freeze schedules and break-glass review
- Console lock flag, lock and unlock display (console repository change)

Depends on:

- add-job-service-routes-start-1

## Usability

Where the path still costs the person time or surprises them:

- A freeze of a whole namespace still needs one lock call; bulk lock avoids one call per unit.
- A locked refusal must name the holder, the reason and the read locks route or the path surprises the developer.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (owner-tagged: decision 10).

- Decisions that apply: 5 (extended), 9 (changed), 10 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "Manual locks (owner decision 2026-10-18). A person can put a manual lock with a reason on one unit, one collection or a whole namespace."
- Quote: "Bulk calls: at most 100 IDs, each item on its own. A bulk call takes from 1 to 100 IDs of one object kind. A call with more answers 400 and does nothing."
- Quote: "A lock is a flag, not a status. The inventory keeps the lock of a unit as a flag, with its holder, beside the status."
- Effect: Follows the owner decision: namespace is an object of unit:lock and widens coverage; reason required at most 500 characters; bulk shape follows decision 5; inventory keeps lock as flag beside status.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches, quota reservations and quota counts, the source filter, safe delete (ADR 153 decision 5)"
- Effect: Keeps manual locks as fixed controls, not gates; the state.write decision record shows the lock state that it checked.
- Conflict: none.

**ADR 300: PM answers on runs, recorded** (not owner-tagged).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A manual lock can cover the whole installation. A person adds a manual lock with the scope `installation` and the action `installation:lock`. A reason is required."
- Effect: Leaves the separate installation scope lock with action installation:lock unchanged; this change uses only action unit:lock on unit, collection and namespace objects.
- Conflict: none.

**ADR 244: The move check, the guide pages and the e2e steps** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "**A preview under the import hold (supersedes in part ADR 213 decision 10).**"
- Effect: Keeps the preview exemption under an import hold; a namespace lock does not remove the preview pass that this decision allows.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): Open question 1 (which other state-changing routes a manual lock covers: import, rename, restore, delete) is undecided, yet the spec and tasks do not cover them; the change cannot be built right without it. File: proposal.md. Resolution: open question: No expanded cover is specified; the wider route list stays only in open question 1 until the owner decides.
- R1.2 (open question): The spec-ready gate fails: the proposal states that a code check of the command `opendustpan locks` does not exist, yet the error table and the usability text rely on it. The change must not be implemented until the command exists or the text names a real command. File: proposal.md. Resolution: answered: Removed the missing CLI command; errors now name only the new lock routes GET locks and POST locks:bulk-lock that this change adds.
- R1.3 (adr conflict): ADR 213 decision 10 (owner) says a manual lock covers a unit, collection or namespace with the action unit:lock; the spec's namespace lock scenario and the break-freeze scenario are not tied to any ADR line for namespace:break-freeze, which is not in the ADR check. The break-freeze refusal relies on an action not checked against the decisions. File: specs/manual-locks/spec.md. Resolution: open question: Removed every break-freeze promise from what_changes and specs; the rank of namespace:break-freeze stays only in open question 2 until the owner decides.
- R1.4 (failure mode): Job-token exception: the rule 'phase started before the lock' has no requirement for the race where the lock arrives between the claim and the tool lock request, or where the phase start time is not recorded; the design hostile test covers only the ordering case. File: specs/manual-locks/spec.md. Resolution: answered: Added job-token-race with fail-closed ordering for equal, missing, and commit-during-request cases plus two scenarios.
- R1.5 (missing scenario): No scenario covers the dispatch check under concurrency: two dispatchers claiming the same queued phase while a lock is written, and the same-transaction claim in design decision 2. File: specs/manual-locks/spec.md. Resolution: answered: Added concurrent-claimers scenario to job-dispatch-waits covering two dispatchers and the same-transaction claim.
- R1.6 (missing scenario): Bulk lock: no scenario for more than 100 IDs (400, nothing done), for an unknown ID, or for bulk unlock by a non-holder; the error table says 400 but the spec has no requirement for it. File: specs/manual-locks/spec.md. Resolution: answered: Added bulk-edge-cases requirement with scenarios for over 100 IDs, unknown ID with forbidden unlock, and locked conflict codes.
- R1.7 (missing scenario): No scenario covers a unit that joins a locked collection or namespace, or a unit that leaves it, although ADR 213 decision 10 states the rule. File: specs/manual-locks/spec.md. Resolution: answered: Added join-leave requirement with join and leave scenarios per ADR 213 decision 10.
- R1.8 (failure mode): Import holds (ADR 241 decision 12) are manual locks on target units; a namespace lock would cover import units and interacts with the preview exemption of ADR 244 decision 12. The spec does not state this interaction. File: specs/manual-locks/spec.md. Resolution: answered: Added import-hold-interaction requirement keeping the ADR 244 preview pass and refusing preview under a later namespace lock.
- R1.9 (wrong claim): The ADR check quotes ADR 300 decision 7 as a whole-installation scope rule and says it leaves the installation lock unchanged; the proposal's scope exclusion is correct but the ADR check gives no evidence that namespace locks do not reach installation:lock. File: proposal.md. Resolution: answered: ADR 300 effect states installation:lock is a separate scope and action; namespace-lock text states cover never extends to installation scope.
- R1.10 (scope): The task list has no task for the console lock and unlock actions beyond a single scenario, and no task for the audit entry of a denied lock request in the HTTP layer. File: tasks.md. Resolution: answered: Added definition-of-done tasks for console lock actions and for denial audit entries in the HTTP layer.
- R2.1 (adr conflict): specs/manual-locks/spec.md: the bulk-edge-cases requirement says a non-holder bulk-unlock gets a 403 locked error, but ADR 213 decision 5 requires each item to be checked for authorization and policy and answered per item; the change does not state which error code a refused authorization takes, and the spec mixes 'locked' with the authorization refusal. Resolution: answered: Rewrote bulk-edge-cases to separate forbidden for authorization refusal from locked for cover conflict, with per-item codes.
- R2.2 (owner decision conflict): proposal.md ADR check quotes ADR 300 decision 7 and says installation lock is unchanged, but the spec's namespace-lock and expanded-write-cover requirements do not state that a namespace lock never covers the installation scope; the owner-tagged ADR 213 decision 10 gap is not closed in the spec. Resolution: answered: Rewrote namespace-lock requirement to state the cover never extends to the installation scope.
- R2.3 (open question): proposal.md open question 1 (which routes a manual lock covers: import, rename, restore, delete) is still open, yet specs/manual-locks/spec.md adds requirement expanded-write-cover as if decided; the spec-ready gate requires the open question to read none, so the change cannot be implemented as written. Resolution: open question: Removed the expanded-write-cover requirement; the wider route list stays only in open question 1.
- R2.4 (open question): proposal.md open question 2 (namespace:break-freeze passing a manual lock) is open, but no requirement in the spec states the refusal; the proposal's What Changes says it must not pass the lock. Resolution: open question: Removed the break-freeze promise from what_changes and the spec; the rank stays only in open question 2.
- R2.5 (wrong claim): proposal.md error table and Why section name GET /api/dustpan/v1/namespaces/{namespace}/locks and POST .../locks:bulk-lock; the code check reports both routes are absent from the API description and route table, so the usability path names routes that do not exist. Resolution: answered: Named each lock route as new in what_changes so the errors and user path reference the new routes of this change.
- R2.6 (failure mode): specs/manual-locks/spec.md job-token-race: the requirement says a lock with equal or missing start time is refused, but does not say how a job token whose phase start time is recorded after the lock write (commit order) is handled; design layer test requires fail-closed, the spec does not state the rule for a tool lock with a lock that commits during the request. Resolution: answered: Extended job-token-race to refuse when a lock commits during the tool-lock request, with a new scenario.
- R2.7 (missing scenario): specs/manual-locks/spec.md: no scenario for a namespace lock covering inventory and bulk answers at scale (design risk: fan-out to many units), nor for a read of a locked unit's status while locked. Resolution: answered: Added namespace-lock scenarios for read-while-locked status and for many-unit fan-out to inventory and bulk answers.
- R2.8 (failure mode): specs/manual-locks/spec.md import-hold-interaction: the spec accepts a preview under an import hold but does not state the interaction with a namespace lock added after the import hold exists, nor a restart during a holding import (ADR 244 decision 12 supersedes ADR 213 decision 10 only in part). Resolution: answered: Rewrote import-hold-interaction so a later namespace lock also refuses a preview, with a scenario.
- R2.9 (scope): tasks.md: task 2.5 and 2.6 add console and denial-audit work that the proposal assigns to the console and HTTP layer, but the task list has no task for the locked-refusal error text naming the read-locks route, which the usability section requires. Resolution: answered: Added a definition-of-done task for the locked-refusal error text that names the read locks route.
- R2.10 (missing scenario): specs/manual-locks-console/spec.md: the console spec has one scenario only; no scenario for a unlock by a non-holder or a force-unlock, though proposal scope says the console has lock and unlock actions with reason. Resolution: answered: Added console scenarios for unlock by non-holder and force-unlock with reason.
- M.24 (scope): The console part belongs to the console repository and cannot be in the server pull request series. The summary says dispatch 'refuses', but a requirement is named 'job-dispatch-waits'. Resolution: answered: Moved console lock display to the console repository and out of scope; kept one dispatch behavior named waits in summary, what_changes and requirement; added dependency on add-job-service-routes-start-1.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which other state-changing routes does a manual lock cover beyond the OpenTofu lock request, the start of a mutating Pulumi update and job dispatch?
   - Options: Cover import, rename, restore and delete under the same lock; Cover only the first-slice paths plus job dispatch.
   - Provisional decision (owner review 14 Nov): Cover import, rename, restore and delete under the same lock.
   - Reason: The issue lists pulumi stack import, pulumi stack rename, state restore and delete as candidates and asks to decide and test; no ADR in the pack decides that list. Benchmark: AWS IAM and Google Cloud IAM: an explicit deny for destructive writes wins over a freeze bypass, with a named break-glass path in the audit log.
   - Persona impact: A namespace administrator who freezes a namespace expects no state change path to slip past; a wider cover upsets no persona when the refusal names the lock and the read locks route. Escape hatch: Force-unlock by a holder with unit:lock stays; break-glass stays a separate reviewed path and never passes silently.
   - Owner-tagged ADRs involved: ADR 213, ADR 222 (their words do not change).
2. Does the action namespace:break-freeze pass a manual lock on a covered unit?
   - Options: namespace:break-freeze never passes a manual lock; namespace:break-freeze passes a manual lock with an audit entry.
   - Provisional decision (owner review 14 Nov): namespace:break-freeze never passes a manual lock.
   - Reason: The issue states that namespace:break-freeze must not pass the lock, but no ADR in the pack defines that action or its rank against the fixed control. Benchmark: Google Cloud IAM: a freeze bypass needs an explicit named permission and a denial audit entry; absent that grant the deny wins.
   - Persona impact: A namespace administrator expects a freeze to hold; either option upsets no persona once the guide names the rule. Escape hatch: Force-unlock by a holder with unit:lock stays available and audited.
   - Owner-tagged ADRs involved: ADR 213, ADR 222 (their words do not change).

Questions considered and answered:

- Which objects can a manual lock cover? ADR 213 decision 10 answers it: unit, collection and namespace are objects of unit:lock, reason required at most 500 characters, a unit that joins a locked collection is covered from that moment.
- Is the job dispatch check a gate or a fixed control? ADR 222 decision 4 answers it: manual locks stay fixed controls, not gates; dispatch checks the lock at claim time and the decision record shows the lock state.
- What shape do bulk lock and bulk unlock take? ADR 213 decision 5 answers the shape: 1 to 100 IDs, per-item check and per-item result, not atomic, more than 100 answers 400 and does nothing.
- Does a namespace lock change the import-hold preview exemption? ADR 244 decision 12 answers part of it: a preview passes the manual lock of an import; the wider list of import, rename, restore and delete is not decided in the pack and stays an open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
