---
version: 10
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
| A job token writes state outside its phase | 403: The credential of the caller may not make this write. The server log names the job, the phase and the gate message. | `kubectl logs -l app=opendustpan --tail=100` |
| A job token writes the state of a unit outside its job | 403: The credential of the caller may not make this write. The server log names the job and the unit. | `kubectl logs -l app=opendustpan --tail=100` |

Evidence for the claims above:

- The gate package defines JobClaim with PassedApply. Proof: pkg/gate/service.go:23 holds the type JobClaim; pkg/gate/service.go:27 holds PassedApply.
- The gate service refuses a write of a phase that did not pass change.apply, except the lock of its plan. Proof: pkg/gate/service.go:323 holds the message: phase did not pass change.apply, and writes only the lock of its plan.
- The server wires a job claimer for runner polls. Proof: pkg/dustpan/runners.go:51 holds type JobClaimerFunc and pkg/dustpan/runners.go:56 holds WithJobClaimer; pkg/dustpan/builtin_stores.go:396 calls it.
- The gate subject of a job token has no job today. Proof: `pkg/dustpan/stateguard.go:402` (`principalSubjects.Subject`) sets `Job: nil` for each credential, and `pkg/gate/service.go:308` (`jobTokenWrite`) refuses a nil `Job`, so each job-token state write is refused on main.
- The job token principal carries the job ID. Proof: the job token service sets `JobID` on the principal of a verified token, and `identity.Principal` holds the field `JobID`.
- The job store reads a job by ID. Proof: the interface `job.Store` has the method `Get`, which returns the job by ID or `job.ErrNotFound`.

## What Changes

- Add a gate subject source in `pkg/dustpan` (NEW `jobClaimSubjects`) that wraps `principalSubjects`. For a job-token principal it reads the job by `Principal.JobID` from the job store on each call and sets `Subject.Job` (job, phase, PassedApply, units). `GuardStateWrite` already asks the subject source on each write, so the claim is fresh on each route that calls the guard on main.
- After the job model allows a write, run the gates of state.write for the job token as for a person: read the fence of the namespace, write the decision record, refuse on `deny`. A job token has no break-glass pass (ADR 395).
- Refuse with identity.ErrDenied any other write of a phase that did not pass change.apply, any write for a unit outside its job, and any write whose claim does not resolve.

Out of scope:

- New decision points or gate outcomes
- Changes to access-limit evaluation for job tokens
- Console display of decision records
- A new read route for decision records
- Unit-import operation kind and the HTTP backend path
- A guard call on the write paths that `stategate.UncheckedPaths` lists on main (Pulumi update create and start, TFE state-version create without a lock, the HTTP backend write of a job that holds its plan lock). A later change adds each check and moves the path to `CheckedPaths`. Until then a job token on those paths meets no state.write gate.

Depends on:

- add-job-session-paths-runner-token-issuer

## Usability

Where the path still costs the person time or surprises them:

- The runner log shows only a denial; the person reads the server log to find the job and phase.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- GuardStateWrite is the gate entry for state.write. (`pkg/gate/statewrite.go:66`)
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
- Effect: The plan-lock exception does not pass a deny gate. An unlocked-write of a job token meets state.write under the same rule. ADR 395 (owner, 2026-11-23) extends decision 1 to a write with a job token.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What may a job token write before its phase passes change.apply? Issue #1254 gives the rule: only the lock or lease of its plan. Any other write fails with identity.ErrDenied.
- Do access limits change for job tokens? No. Issue #1254 says they already hold for job tokens.
- Does a deny gate refuse a write with a job token that the job model allows, the plan lock included? Yes. ADR 395 decision 1 (owner, 2026-11-23): the gates run after `jobTokenWrite` allows the write, and a `deny` gate refuses it. A job-token write that reaches the gates writes a decision record (ADR 395 decision 2).
- How does a job-token unlocked-write meet state.write? ADR 335 decision 3: with operation kind unlocked-write; a refusal refuses the write.
- Where does the claim come from on main? No source sets gate.Subject.Job: principalSubjects at pkg/dustpan/stateguard.go:402 sets nil. pkg/dustpan/runners.go:45 builds a job.Claimer for runner polls, which is not a state-write seam. The change adds a subject source that reads the job store; it adds no second port and no field on gate.StateWrite.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
