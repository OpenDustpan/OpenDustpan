---
version: 3
---

Issue: #1254.

## Why

**Persona: runner operator.** A runner operator runs jobs on runners with job tokens. A token of a plan phase must not write the state of another unit, or write state at all, because a wrong-phase write corrupts state with no clear record.

The user path this change must make work:

1. **API.** A runner starts a plan or apply job and writes state with its job token through the Pulumi or TFE-compatible API.
2. **API.** A write outside the phase of the job is refused with 403.
3. **Docs page.** The person reads the state.write page for the job-token rule and the check command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A job token writes state outside its phase | 403 denied: the phase of the job did not pass change.apply and the write is not the lock or lease of its plan. | `kubectl logs -l app=opendustpan --tail=100` |
| A job token writes the state of a unit outside its job | 403 denied: the job token names a unit outside its job. | `kubectl logs -l app=opendustpan --tail=100` |

Evidence for the claims above:

- The gate package defines JobClaim with PassedApply. Proof: pkg/gate/service.go:23 holds the type JobClaim; pkg/gate/service.go:27 holds PassedApply.
- The gate service refuses a write of a phase that did not pass change.apply, except the lock of its plan. Proof: pkg/gate/service.go:276 holds the message: phase did not pass change.apply, and writes only the lock of its plan.
- The server wires a job claimer for runner polls. Proof: pkg/dustpan/runners.go:45 holds type JobClaimerFunc and pkg/dustpan/runners.go:47 holds WithJobClaimer; cmd/opendustpan/main.go:1295 calls it.
- The state-write adapters cannot build a JobClaim today. Proof: Issue #1254 body: Service.GuardStateWrite lets a job token pass the gates, with no check of the job, because the adapters cannot build a JobClaim.

## What Changes

- Add a source of JobClaim (job, phase, PassedApply, units) for a verified job token, resolved fresh for each call, and pass it to Evaluate at state.write on the Pulumi and TFE-compatible paths.
- Refuse with identity.ErrDenied any other write of a phase that did not pass change.apply, any write for a unit outside its job, and any write whose claim does not resolve.

Out of scope:

- New decision points or gate outcomes
- Changes to access-limit evaluation for job tokens
- Console display of decision records
- A new read route for decision records
- Unit-import operation kind and the HTTP backend path

Depends on:

- add-job-session-paths-runner-token-issuer

## Usability

Where the path still costs the person time or surprises them:

- The runner log shows only a denial; the person reads the server log to find the job and phase.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- GuardStateWrite is the gate entry for state.write. (`pkg/gate/statewrite.go:65`)
- JobClaim carries PassedApply. (`pkg/gate/service.go:23`)
- JobClaimerFunc builds the claimer of a runner poll. (`pkg/dustpan/runners.go:45`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 16).

- Decisions that apply: 16 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Effect: The change carries out decision 16 for job tokens by resolving a JobClaim and passing it to Evaluate. No owner-tagged wording changes.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A deny gate on a cancel or a force-unlock stays (owner, 2026-11-07)."
- Quote: "Such a write meets `state.write` before its transaction. Its operation kind is `unlocked-write`. A refusal refuses the write."
- Effect: The plan-lock exception does not pass a deny gate. An unlocked-write of a job token meets state.write under the same rule.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What may a job token write before its phase passes change.apply? Issue #1254 gives the rule: only the lock or lease of its plan. Any other write fails with identity.ErrDenied.
- Do access limits change for job tokens? No. Issue #1254 says they already hold for job tokens.
- Does a deny gate still refuse a plan-lock write? Yes. ADR 335 decision 1: a deny gate keeps its effect.
- How does a job-token unlocked-write meet state.write? ADR 335 decision 3: with operation kind unlocked-write; a refusal refuses the write.
- Where does the claim come from on main? No gate.JobClaim source exists in the adapters. pkg/dustpan/runners.go:45 builds a job.Claimer for runner polls. The change adds a new source file and reuses job data; it adds no second port.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
