---
version: 2
---

Issue: #1254.

## Why

**Persona: runner operator.** A runner operator runs jobs on runners with job tokens. A token from a plan phase must not write state of another unit or an apply, or a stale or wrong phase corrupts state with no clear record.

The user path this change must make work:

1. **API.** Runner starts a plan or apply job with its job token and writes state through the Pulumi or TFE-compatible API.
2. **API.** An out-of-phase write is refused; the person checks the denial with the logged grep for state.write denied.
3. **Docs page.** The person reads the state.write page for the job-token rule and the check command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job token writes state outside its phase | 403 denied: the phase of the job did not pass change.apply and the write is not the lock or lease of its plan. Check the server log for the denial. | `kubectl logs -l app=opendustpan --tail=100 \| grep "state.write denied"` |
| Job token writes the state of a unit outside its job | 403 denied: the job token names a unit outside its job. Check the server log for the denial. | `kubectl logs -l app=opendustpan --tail=100 \| grep "state.write denied"` |

Evidence for the claims above:

- The gate package defines JobClaim with job, phase, PassedApply, and units. Proof: pkg/gate/service.go:21:// JobClaim is the job of a job token: the job, its phase, and the units of
- The gate service refuses a write when the phase did not pass change.apply except for the lock of its plan. Proof: pkg/gate/service.go:276:			"%w: phase %s of job %s did not pass change.apply, and writes only the lock of its plan",
- The server wires a job claimer for the gate guard. Proof: cmd/opendustpan/main.go:1295:			dustpan.WithJobClaimer(func(g job.GateGuard) job.Claimer {
- Issue #1254 states the job-token rule and the refusal for this change. Proof: Part of #899. Follows #1032.

## What Changes

- Build a JobClaim of job, phase, PassedApply, and units for each job token at state.write, resolved fresh for each call, and evaluate the gates with it on the Pulumi and TFE-compatible paths including unlocked-write.
- Refuse with identity.ErrDenied any other write from a phase that did not pass change.apply, any write for a unit outside its job, and any write whose claim does not resolve.

Out of scope:

- New decision points or new gate outcomes
- Changes to access-limit evaluation for job tokens
- Console display of decision records
- A new read route for decision records
- Unit-import operation kind and HTTP backend path

Depends on:

- add-job-session-paths-runner

## Usability

Where the path still costs the person time or surprises them:

- A runner log shows only a denial; the person runs the logged grep for state.write denied to find the job and phase, which still costs time on a busy runner.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- GuardStateWrite is the gate service entry for state.write. (`pkg/gate/statewrite.go:65 (func Service.GuardStateWrite)`)
- Evaluate is the gate engine call that the fix passes the JobClaim to. (`pkg/gate/cedargo/cedargo.go:135 (func Engine.Evaluate)`)
- JobClaim carries job, phase, PassedApply, and units. (`pkg/gate/service.go:23`)
- PassedApply marks a phase that passed change.apply. (`pkg/gate/service.go:27`)
- Out-of-phase writes other than the plan lock fail with the change.apply message. (`pkg/gate/service.go:276`)
- Server wires a job claimer for the runner poll and gate guard. (`cmd/opendustpan/main.go:1295`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 16).

- Decisions that apply: 16 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Effect: The change carries out decision 16 at state.write by resolving a JobClaim for each job token and passing it to Evaluate; access limits keep their hold on job tokens and no owner-tagged wording changes.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A deny gate on a cancel or a force-unlock stays (owner, 2026-11-07). A `deny` gate at `state.write` can refuse a Pulumi update cancel or a force-unlock. The gate keeps its effect. No rule passes it."
- Quote: "An unlocked state version on the TFE-compatible API (owner, 2026-11-07). Such a write meets `state.write` before its transaction. Its operation kind is `unlocked-write`. A refusal refuses the write."
- Effect: The plan-lock exception does not pass a deny gate on cancel or force-unlock, and an unlocked-write from a job token meets state.write with operation kind unlocked-write under the same job-token rule; no owner-tagged wording changes.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: error code `state.write` (.errors[0].command) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: task "Run an e2e step with a runner plan and apply for each tool with a job-" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Pass the definition of done: wiring, route actions, e2e step, docs pag" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- What may a job token write before its phase passes change.apply? Issue #1254 states the rule in full: a phase that did not pass change.apply writes only the lock or lease of its plan, and any other write fails with identity.ErrDenied. The pack truncates the table of ADR 224 decision 16, so the ADR check quotes the exact leading span.
- What refusal and access-limit behavior applies to job tokens at state.write? Issue #1254 names identity.ErrDenied as the refusal and states the access limits already hold for job tokens, so access-limit evaluation is unchanged.
- How does the plan-lock exception meet a deny gate on cancel or force-unlock and a manual lock? ADR 335 decision 1 states a deny gate keeps its effect and no rule passes it, so the plan-lock exception does not pass such a gate. Manual locks are checked with the gates before the exception allows the lock write.
- How does a job-token unlocked-write meet state.write? ADR 335 decision 3 states an unlocked TFE write meets state.write with operation kind unlocked-write and a refusal refuses the write, so it follows the same JobClaim rule.
- Does the change reuse the existing job Claimer or add a second one? The existing job.Claimer wired with dustpan.WithJobClaimer is reused; no second claimer port is added. The unexported adapter holds the resolved claim for one call.
- Which tool paths are in scope for the job-token rule? Only the Pulumi and TFE-compatible state-write paths. The unit-import kind and the HTTP backend path are unchanged.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
