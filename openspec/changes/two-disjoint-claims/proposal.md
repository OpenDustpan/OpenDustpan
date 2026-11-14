Roadmap: `docs/roadmap.md:634` (Two disjoint claims).

## Why

**Persona: developer.** Developer on two teams changes different resources of one workspace at the same time. Each team runs a runner job with a saved plan. The developer needs both claims accepted before anything runs, so parallel work starts from a clear record.

The user path this change must make work:

1. **API.** Runner job A plans the workspace and registers its non-replace claim with its address set through the new claims route.
2. **API.** Runner job B plans and registers its disjoint non-replace claim through the same route.
3. **API.** Developer lists claims of the workspace and sees two records in state accepted with start time and principal.
4. **E2e.** The e2e suite runs two runner jobs with real OpenTofu against a fake cloud provider and checks two accepted claim records.
5. **Docs page.** A guide describes the claim record fields and the list call for two disjoint claims.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Claim base is touched by a later version on an address of the claim. | Server answers 422 with the touching version and names the current state version. Plan again from the current state version, then register a new claim. | `tofu plan` |
| Claim overlaps an accepted claim. | Server answers 409 with the blocking claim ID and stores no claim. Wait for the blocking claim to end, plan again, then register a new claim. | `GET /api/dustpan/v1/workspaces/{id}/claims` |
| Claim holds a replace action. | Server answers 409 with the code `claim.closure-unproven` and stores no claim. This change does not build the closure test, and a closure that the server cannot prove is a conflict. Wait for the accepted claims to end, then register again. | `GET /api/dustpan/v1/workspaces/{id}/claims` |
| Principal lacks unit:claim-register on the workspace. | Server answers 403 and names the missing action on the workspace. Ask a namespace administrator for the grant, then register again. | `GET /api/server/principals/{}/grants` |
| Claim holds an empty resource set. | Server answers 422 and changes nothing. Plan with at least one non-replace resource change, then register again. | `tofu plan` |
| Claim names a missing, deleted or locked workspace or an unknown base. | Server answers 404 for a missing workspace, 410 for a deleted workspace and 422 for a locked workspace or an unknown base version. Use the current workspace and base, then register again. | `GET /api/dustpan/v1/workspaces/{id}/claims` |

Evidence for the claims above:

- Runner jobs run through the runner protocol and the runner management API. Proof: Pack code lists internal/api/runnerapi and pkg/runner.
- Claim gate behavior has e2e coverage today. Proof: Pack e2e list names e2e/claim_gate_test.go.
- Job plan and jobs API paths have e2e coverage today. Proof: Pack e2e list names e2e/jobs_plan_test.go and e2e/jobs_api_test.go.

## What Changes

- New route POST /api/dustpan/v1/workspaces/{id}/claims: a runner job or local wrapper registers a non-replace claim with base state version, address set and principal.
- New route GET /api/dustpan/v1/workspaces/{id}/claims: lists each claim with state, base, address set, principal, job ID, start time and end time.
- Server accepts a second non-replace claim when its address set is disjoint from each accepted claim.
- NEW: the job token of the apply phase is bound to the job and its apply claim (ADR 369 decision 1); a request with the token of another job, or after the claim ended, is refused.
- NEW: the claim record and its audit entry name the actor, the job, the apply claim and the trigger (ADR 369 decision 5).
- Server refuses an overlapping non-replace claim with 409, and a replace claim with 409 `claim.closure-unproven` (ADR 368 decision 6: an unproven closure is a conflict).
- Each accepted claim writes one audit entry, and each claim marks ended with an end time at job end or lease expiry.

Out of scope:

- Concurrent applies and per-resource merge of uploads.
- Blocked job status mapping of a refused claim.
- Strict cases that keep the whole-unit lock.
- Local CLI wrapper retry of refused claims.
- Pulumi CLI claim model and client-encrypted state.
- Serial assignment under a claim, parent pointer and claim fence (ADR 368 decisions 2 to 4, built in concurrent-applies-merge-core and concurrent-applies-merge-history-audit).
- The closure test that can prove a replace claim disjoint (ADR 368 decision 6).

Depends on:

- ADR 368 on main (pull request #3052)
- ADR 369 on main (pull request #3056)

## Usability

Where the path still costs the person time or surprises them:

- Developer reads two claim IDs with state accepted, start time and principal to confirm both jobs proceed.
- A replace claim gets a refusal that says the closure is not proven yet.
- No local run retry path exists in this step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Disjoint behavior has a conformance test in the workspace package. (`pkg/workspace/reveal_conformance_test.go:1463`)
- A workspace locked error code exists for whole-unit lock cases. (`internal/errcode/catalog_workspace.go:15`)
- Runner claimed-operation behavior has a test in the durable package. (`pkg/durable/runner_claimed_test.go:16`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 3 (changed), 6 (extended).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2; ADR 368 changes decision 3. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 368, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220, 368.
- Quote: "A new state version needs the lock of the caller."
- Quote: "The server mints a lock ID for each acquisition."
- Effect: Claim registration grants no lock ID and publishes no state version. The lock, serial and lineage checks stay unchanged.
- Conflict: none.

**ADR 369: The actor of a job: trust rules, conditions on token claims, and external service principals** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A job acts through a short-lived credential that is bound to the job and to its apply claim."
- Quote: "A TFE-compatible request names its apply claim through the job token."
- Effect: The register route binds the apply claim to the job token of the phase. The record names the actor, the job and the apply claim.
- Conflict: none.

**ADR 368: Concurrent applies: apply claims, the server serial, fences and the closure test** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An **apply claim** is the reservation of the resource instances that one saved plan changes and reads."
- Quote: "Each apply claim has a lease."
- Quote: "When the check cannot prove an edge, the answer is a conflict."
- Effect: The claim record is an apply claim with a stable ID and a lease. A replace claim is a conflict until the closure test lands.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current), 6 (current).
- Changed by: ADR 368 extends decision 6; ADR 369 extends decision 6. Chain to the end: ADR 368, ADR 369.
- Current rule: this ADR, with ADR 368, 369.
- Quote: "A runner job registers its saved plan as a claim before the apply."
- Quote: "OpenTofu is first."
- Quote: "The code of milestone 8 comes after the project owner accepts that RFC as an ADR."
- Effect: ADR 368 answers decision 6, except one principal for each job on the TFE-compatible path. This change records two disjoint runner-job claims and refuses overlapping ones, with no merge and no serial assignment. ADR 369 decides the credential of the job.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does claim registration happen in the runner path? ADR 302 decision 2: a runner job registers its saved plan as a claim before the apply.
- Does this change assign serials or merge state under claims? No. ADR 368 decisions 2 and 3 decide them, and concurrent-applies-merge-core builds them.
- Which tool does this step cover? ADR 302 decision 3: OpenTofu first.
- Can code start now? Yes, when ADR 368 and ADR 369 are on main.
- What is the claim ID and the expiry rule? ADR 368 decisions 3 and 4: a stable ID for each apply claim, and a lease (the durable claim of the apply phase for a runner job, 90 seconds renewed every 30 seconds for the local wrapper).
- Which credential names the apply claim on the TFE-compatible path? ADR 369 decision 1 (owner): the actor is the person or principal that a stored trust rule resolves, never the job. The job acts through a short-lived credential bound to the job and its apply claim, and a TFE-compatible request names its apply claim through the job token.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
