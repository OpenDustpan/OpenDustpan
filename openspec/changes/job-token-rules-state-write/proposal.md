Issue: #1254.

## Why

**Persona: runner operator.** A runner operator runs jobs on runners with job tokens. A token from a plan phase must not write state of another unit or an apply, or a stale or wrong phase corrupts state with no clear record.

The user path this change must make work:

1. **API.** Runner starts a plan or apply job with its job token and writes state through the Pulumi or TFE-compatible API.
2. **API.** Out-of-phase write is refused; the person checks the runner log and the server log for the job and phase.
3. **Docs page.** The person reads the state.write page for the job-token rule and the check command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job token writes state outside its phase | 403 state.write denied: the phase of the job did not pass change.apply and the write is not the lock or lease of its plan. | `kubectl logs -l app=opendustpan --tail=100 \| grep job` |
| Job token writes the state of a unit outside its job | 403 state.write denied: the job token names a unit outside its job; run the write from the phase of the owning job. | `kubectl logs -l app=opendustpan --tail=100 \| grep job` |

Evidence for the claims above:

- The gate package defines JobClaim with job, phase, PassedApply, and units. Proof: pkg/gate/service.go:21:// JobClaim is the job of a job token: the job, its phase, and the units of
- The gate service refuses a write when the phase did not pass change.apply except for the lock of its plan. Proof: pkg/gate/service.go:276:			"%w: phase %s of job %s did not pass change.apply, and writes only the lock of its plan",
- The server wires a job claimer for the gate guard. Proof: cmd/opendustpan/main.go:1295:			dustpan.WithJobClaimer(func(g job.GateGuard) job.Claimer {

## What Changes

- Build a JobClaim of job, phase, PassedApply, and units for each job token at state.write, resolved fresh for each call, and evaluate the gates with it on the Pulumi and TFE-compatible paths including unlocked-write.
- Refuse with identity.ErrDenied any other write from a phase that did not pass change.apply, any write for a unit outside its job, and any write whose claim does not resolve.

Out of scope:

- New decision points or new gate outcomes
- Changes to access-limit evaluation for job tokens
- Console display of decision records
- A new read route for decision records

Depends on:

- add-job-session-paths-runner

## Usability

Where the path still costs the person time or surprises them:

- A runner log shows only a denial; the person must read the server log to find the job and phase, which costs time on a busy runner.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 16).

- Decisions that apply: 16 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Effect: The change carries out the job-token rule of decision 16 at state.write by resolving a JobClaim for each job token and passing it to Evaluate; access limits keep their current hold on job tokens and no owner-tagged wording changes.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A deny gate on a cancel or a force-unlock stays (owner, 2026-11-07). A `deny` gate at `state.write` can refuse a Pulumi update cancel or a force-unlock. The gate keeps its effect. No rule passes it."
- Quote: "An unlocked state version on the TFE-compatible API (owner, 2026-11-07). Such a write meets `state.write` before its transaction. Its operation kind is `unlocked-write`. A refusal refuses the write."
- Effect: The plan-lock exception does not pass a deny gate on cancel or force-unlock, and an unlocked-write from a job token meets state.write with operation kind unlocked-write under the same job-token rule; no owner-tagged wording changes.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The proposal's Open questions section is not 'none': it reports failing code checks on the route GET /api/dustpan/v1/namespaces/{namespace}/decision-records, which is absent from the API description and route table, so the spec-ready gate is not met and the user path step 2 and error table depend on a non-existent route. Resolution: answered: Removed the decision-records route from errors and user path; what_changes and out_of_scope now state that no new read route belongs to this item.
- R1.2 (wrong claim): The error table and the user path name a decision-records route that the input does not support; the proposal's API step cannot be built as written. Resolution: answered: Error table and user path no longer name a decision-records route; checks use the server log and the job of the token.
- R1.3 (adr conflict): ADR 224 decision 16 is owner-tagged and changed; the proposal cites only the state.write quote and does not quote the job-token lines of decision 16 that apply, so the owner-tagged conflict check is not evidenced. Resolution: answered: ADR check now quotes the available decision 16 text character for character and the effect states the job-token rule from issue #1254 is carried out with no change to owner-tagged wording.
- R1.4 (missing scenario): No scenario covers a job token whose claim source fails or is absent in the adapter path for Pulumi versus TFE routes; the design risk says fail closed but no scenario or test names the refusal code for that case. Resolution: answered: Added requirement Unresolvable job claim fails closed with a scenario for a claim source failure on each tool path.
- R1.5 (failure mode): The design says a job token without a resolvable job fails closed and may block a running job, but no requirement says how a blocked running job recovers or what the runner sees; the usability note only mentions a denial. Resolution: answered: Added recovery rule: the refusal ends only the write, the job stays, and the runner sees the denial in the job log and retries after the phase passes change.apply.
- R1.6 (missing scenario): The spec has no scenario for the plan-lock or lease exception itself: a plan job writing its own lock must be allowed, and no scenario tests that it is allowed. Resolution: answered: Added scenario Plan phase writes its own lock that the server allows.
- R1.7 (failure mode): Concurrency: a job token's phase or PassedApply could change between claim resolution and Evaluate in a multi-write job; no requirement fixes the claim's lifetime per request. Resolution: answered: Added requirement Claim lifetime is one write request with a scenario for a phase change between writes.
- R1.8 (open question): The unlocked-write route (ADR 335 decision 3, owner) for the TFE API from a job token is not mentioned, though the design covers TFE state-write paths. Resolution: answered: Design context and requirement text now state the same job-token rule covers the TFE-compatible unlocked-write path under ADR 335 decision 3 with no separate behavior.
- R1.9 (scope): Tasks 2.2 and 3.2 cover docs and wiring, but the proposal's out-of-scope list does not state whether the decision-record read route belongs to this item, so the route work is unassigned. Resolution: answered: Out_of_scope now states a new read route for decision records is not part of this item, and tasks no longer imply that route work.
- R1.10 (wrong claim): The ADR check claims ADR 224 has no conflict, yet the change moves job-token writes through the gate, which changes ADR 224 decision 16 behavior; the conflict line is not evidenced. Resolution: answered: ADR check effect now states the change carries out decision 16 rather than changing it, with conflict none and no edit to owner-tagged wording.
- R2.1 (adr conflict): ADR 224 decision 16 is owner-tagged and changed; the change claims no conflict, but ADR 335 decision 3 (owner) states the unlocked-write refusal for TFE and the spec only mentions it in passing; the owner-tagged rule for unlocked state versions must be quoted and checked, not assumed. File: openspec/changes/job-token-rules-state-write/proposal.md Resolution: answered: Added ADR 335 to the ADR check with owner-tagged decisions 1 and 3 quoted character for character, and stated the unlocked-write and deny-gate effects with no wording change.
- R2.2 (wrong claim): The ADR check quotes only the first line of ADR 224 decision 16 and states the effect as carrying out the rule, but the quoted text is not the decision text in the input (the input decision 16 text is truncated), so the quote cannot be verified. File: openspec/changes/job-token-rules-state-write/proposal.md Resolution: answered: Kept the ADR 224 decision 16 quote as the exact leading span the pack gives and recorded in considered why only that span is quoted, with issue #1254 stating the full job-token rule.
- R2.3 (missing scenario): No scenario covers a job token write whose phase is not known at all (a token without a phase claim), and no scenario covers a job token on a unit of a locked collection or an installation manual lock (ADR 213 decision 10, ADR 300 decision 7) interacting with the plan-lock exception. File: openspec/changes/job-token-rules-state-write/specs/gate-state-write/spec.md Resolution: answered: Added scenarios for a token with no phase claim and for a cancelled or deleted job, and stated the plan-lock exception does not bypass manual locks or deny gates.
- R2.4 (failure mode): The plan-lock exception is not bounded: a job token of a plan phase that writes its own lock could also write a lease or a Pulumi update cancel that ADR 335 decision 1 (owner) makes a deny gate; the spec does not say how the exception interacts with a deny gate on cancel or force-unlock. File: openspec/changes/job-token-rules-state-write/specs/gate-state-write/spec.md Resolution: answered: Added requirement that the plan-lock exception still meets deny gates, so a cancel or force-unlock refused by a deny gate stays refused.
- R2.5 (failure mode): The refusal path for an unlocked TFE state version (operation kind unlocked-write, ADR 335 decision 3, owner) from a job token is said to be covered, but the spec has no scenario for it; a refused unlocked write may leave residue per ADR 335 decision 3, which the change does not address. File: openspec/changes/job-token-rules-state-write/specs/gate-state-write/spec.md Resolution: answered: Added unlocked-write job-token scenarios under ADR 335 decision 3 and stated a refusal refuses the write with existing residue behavior unchanged.
- R2.6 (open question): The claim-resolution path for a token whose job was cancelled or whose record was deleted between writes is not defined; the design only covers a claim that does not resolve. File: openspec/changes/job-token-rules-state-write/design.md Resolution: answered: Defined the cancelled or deleted job path as an unresolvable claim that fails closed with identity.ErrDenied and leaves the job record unchanged.
- R2.7 (wrong claim): The proposal says the recovery path is a retry after the phase passes change.apply, but no ADR or issue input supports a runner retry policy; the claim that the runner sees the denial in the job log is unevidenced. File: openspec/changes/job-token-rules-state-write/proposal.md Resolution: answered: Removed the unevidenced runner retry and job-log claims; the recovery text now states only that the refusal ends the write and a later write is evaluated again.
- R2.8 (scope): The error table names kubectl logs for the server log and opendustpan --help for the cross-unit error; the command does not name a check that shows the job and phase, so the one-command rule of CLAUDE.md is not met. File: openspec/changes/job-token-rules-state-write/proposal.md Resolution: answered: Changed both error commands to the server log check filtered by job so the one command shows the job and phase of the denial.
- R2.9 (open question): Evidence for the proposal's 'Open questions: none' relies on quoting ADR 224 decision 16 which the drafter could not quote in full; the gate is therefore not evidenced. File: openspec/changes/job-token-rules-state-write/proposal.md Resolution: answered: Recorded the truncated decision 16 text in considered with the exact-span quote and the full rule from issue #1254 and ADR 335, so Open questions: none is evidenced.
- M.36 (dependency): The JobClaim needs the job token issuer of add-job-session-paths-runner. Resolution: answered: Added depends_on add-job-session-paths-runner: the JobClaim source needs the job token issuer and job session paths from that change; this change only consumes that verified token.

## Open questions

none

Questions considered and answered:

- What may a job token write before its phase passes change.apply? ADR 224 decision 16 as quoted in the ADR check plus issue #1254 state the rule: a phase that did not pass change.apply writes only the lock or lease of its plan, and any other write fails with identity.ErrDenied.
- What refusal and access-limit behavior applies to job tokens at state.write? Issue #1254 names identity.ErrDenied as the refusal and states the access limits already hold for job tokens, so this change keeps access-limit evaluation unchanged.
- How does the plan-lock exception meet a deny gate on cancel or force-unlock? ADR 335 decision 1 as quoted states a deny gate at state.write on cancel or force-unlock keeps its effect and no rule passes it, so the plan-lock exception does not pass such a gate.
- How does a job-token unlocked-write meet state.write? ADR 335 decision 3 as quoted states an unlocked TFE write meets state.write with operation kind unlocked-write and a refusal refuses the write, so a job-token unlocked-write follows the same job-token rule; residue on refusal is existing behavior and unchanged.
- Why does the ADR check quote only the leading span of ADR 224 decision 16? The pack truncates the table of ADR 224 decision 16 after its first line, so the quote is the exact leading span of that decision; issue #1254 states the job-token rule in full and ADR 335 decisions 1 and 3 are quoted in full as the pack gives them.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
