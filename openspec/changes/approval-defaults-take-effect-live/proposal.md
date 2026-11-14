Issue: #1157.

## Why

**Persona: developer.** A developer runs the Pulumi CLI and OpenTofu against the server and must see one predictable approval wait when the namespace requires it, instead of an apply that skips review or blocks without a reason.

The user path this change must make work:

1. **API.** Namespace administrator sets the approval default through the API and reads the docs page.
2. **CLI.** Developer runs a Pulumi stack update and an OpenTofu workspace apply through the tool CLIs against the server.
3. **API.** Developer sees approval wait, a second person approves, and the job continues.
4. **Docs page.** Developer reads the docs page for the default, the wait state and the approve command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply refused for a principal with only plan rights. | 403 with the required action and the record. Run `opendustpan status` to see the job state and the blocking requirement. | `opendustpan status` |
| Apply on a stale plan after a new plan reset approvals. | 409 with the plan revision and the decision record. Run `opendustpan approve <job-id>` to list the approvals still needed on the latest revision. | `opendustpan approve <job-id>` |
| Job held in approval wait with no approver available. | 422 with the gate and the decision record. Run `opendustpan check` to read the blocking requirement and the record. | `opendustpan check` |

Evidence for the claims above:

- Slice 1 stores approval defaults and rules and jobgate is tested but wired nowhere. Proof: Issue #1157 body: Slice 1 stores approval defaults and approval rules (REST routes) and has a `job.Decider` adapter (`pkg/gate/jobgate`) that is tested but wired nowhere, because no job service runs.
- pkg/gate/jobgate implements job.Decider. Proof: rg -n -F --glob '!*_test.go' 'job.Decider' pkg internal cmd matches 4, first lines pkg/gate/jobgate/jobgate.go:39:// Decider is a job.Decider. It allows every start, and adds the approval gate
- No live caller wires jobgate.Decider. Proof: rg -n -F --glob '!*_test.go' 'jobgate.Decider' pkg internal cmd matches 0 with empty first_lines
- The change path exists as a package. Proof: Path pkg/gate/jobgate exists: true.

## What Changes

- Job service evaluates stored approval defaults through jobgate.Decider at change.propose, change.planned and change.apply for Pulumi and OpenTofu jobs.
- Applies with a requirement wait in approval wait and resume after approvals; applies with no default need no approval.
- E2E proves the wait and resume path on both tools with decision records.

Out of scope:

- New approval rule kinds or gate policy language
- Console run page timeline and diff rendering
- Pre-merge apply and pull request automation behavior
- Stored plan hash binding beyond plan staleness reset
- State.write, profile.save and profile.open evaluation
- Distinct-approver counting and starter exclusion (several-approval-requirements-one-change)
- Stale-revision reset semantics (policy-approvals)
- Break-glass dev-data approver rules (gate-evaluation-must-apply-dev)

Depends on:

- add-job-service-routes-start
- several-approval-requirements-one-change
- gate-evaluation-must-apply-dev
- `several-approval-requirements-one-change` (order of the milestone review)
- `gate-evaluation-must-apply-dev` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A developer sees two tool flows with one wait word and one approve command.
- The wait names the requirement, the approvers still needed and the decision record.
- An apply that waits after a default changed surprises the developer; the wait names the new requirement and the record.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 2, 4, 8).

- Decisions that apply: 2 (current), 4 (current), 8 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Quote: "Fixed controls stay fixed (owner)."
- Quote: "Words. "Gate" names the stored rule."
- Effect: The change uses the fixed decision points, the approval wait name, and the plan staleness fixed control.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 2, 13, 14).

- Decisions that apply: 2 (extended), 3 (current), 7 (current), 13 (current), 14 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Six decision points (owner)."
- Quote: "The recheck at `change.apply`. `change.apply` evaluates the gates again with the facts of that moment."
- Quote: "Modes. A gate in `enforce` mode gives its outcome."
- Quote: "An error fails closed (owner)."
- Quote: "Combining (owner). For each unit, in this order: 1. Any `deny` gives `deny`, with the message of each `deny`."
- Effect: The change wires evaluation at change.propose, change.planned and change.apply, keeps combine, recheck-only-adds, evaluate mode and fail-closed error rules.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 1, 3).

- Decisions that apply: 1 (current), 3 (extended).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A fact (owner). A fact is a typed value that the server produces before a decision point."
- Quote: "A missing fact is a typed value (owner)."
- Effect: The change uses stored approval defaults with server-produced facts and fails closed when a needed fact is unavailable.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md Open questions is not 'none': it lists failed code checks for opendustpan auth, job and decision commands, so the spec-ready gate refuses the change until those commands exist or the user path is changed. Resolution: answered: User path uses existing CLI verbs opendustpan status, opendustpan approve and opendustpan check; open questions reads none and what_changes adds no new command.
- R1.2 (missing scenario): specs/job-approval-wait/spec.md has no scenario for a requirement added while a job is in approval wait (design Decision 2 holds and resumes on the same requirement set), nor for an approver who is also the job's principal, which ADR 224 decision 14 and ADR 225 require to be excluded. Resolution: answered: Distinct-approvers and starter-exclusion requirement with scenarios added; recheck-adds requirement covers a requirement added during wait.
- R1.3 (adr conflict): ADR 224 decision 3: "A new requirement puts the change back into the approval wait" and the recheck at change.apply is not covered; the spec only tests start, so a default saved after a job starts is not evaluated at change.apply as the design Risks assume. Resolution: answered: Recheck-apply requirement evaluates change.apply with current facts and returns the change to approval wait on a new requirement.
- R1.4 (adr conflict): ADR 224 decision 2 lists six decision points and ADR 222 decision 2 names change.apply, but the design wires jobgate.Decider at job start and after plan store; the change does not map its points to change.propose, change.planned and change.apply, and ADR 353 adds change.plan, so the wiring points are unsupported. Resolution: answered: Design maps wiring to change.propose, change.planned and change.apply; no change.plan wiring is added.
- R1.5 (failure mode): No requirement covers a stale approval: design layer HTTP handler says stale approvals are refused against a new plan revision, but no spec scenario or task test covers it. Resolution: answered: Stale-plan-refused requirement and failing test task cover replay of a prior-revision approval.
- R1.6 (missing scenario): Refusal path for a deny gate or a gate error (ADR 224 decision 13 fails closed) is not in the spec; an error in enforce mode must stop the change with gate.ErrRuleFailed, not allow it. Resolution: answered: Deny-and-error-fail-closed requirement covers deny refusal and enforce-mode error stop with the record.
- R1.7 (failure mode): Restart or outage while a job is in approval wait: no requirement says whether the wait survives a server restart and how the resume is replayed, and the distinct-approver check after restart is untested. Resolution: answered: Wait-survives-restart requirement covers stored wait and signatures across restart with the distinct-approver check.
- R1.8 (missing scenario): Authorization refusal path: the design layer 2 requires a 403 for apply under plan-only rights, but the spec and tasks contain no scenario or test for it. Resolution: answered: Plan-rights-refused requirement with 403 scenario and service-layer hostile test added.
- R1.9 (open question): Approval defaults that name a mode (evaluate or enforce) are not addressed; ADR 224 decision 7 says a gate in evaluate mode has no effect, but the spec makes every default block. Resolution: answered: Evaluate-mode-no-block requirement records the outcome without wait when only an evaluate-mode default matches.
- R1.10 (scope): Tasks 2.2 and 3.1 cover distinct-approver resume and decision record naming, but the spec has no requirement for either; the proposal's decision-record error is not specified. Resolution: answered: Decision-record-names-gate requirement and record task cover gate versions and schema version in each record.
- R2.1 (adr conflict): ADR 224 decision 2 names six decision points and the design wires only change.propose, change.planned and change.apply; the spec's Recheck at apply and Evaluate mode requirements do not state how state.write and profile points are excluded, and the proposal's wiring is not tied to the Replaces column of decision 2 (job.start, catalog.request) that the change must retire. Resolution: answered: Design maps propose/planned/apply to the Replaces column and retires job.start; state.write and profile points moved to out_of_scope.
- R2.2 (wrong claim): The ADR check quotes ADR 224 decision 14 as 'Combining (owner)' and the proposal cites 'ADR 225' for distinct people, but ADR 225 is not in the decisions file or the ADR check list; the distinct-approver and starter-exclusion rule is claimed without a read ADR. Resolution: answered: Removed ADR 225 read claim; distinct-people rule now quoted only from the ADR 224 decision 14 span that cites ADR 225.
- R2.3 (open question): The spec's stale-plan requirement names the latest plan revision but no ADR or code decides how a plan revision is identified and when an approval counts as prior; ADR 222 fixed control of plan staleness (ADR 102, ADR 191 decision 7) is not referenced in the change. Resolution: answered: Stale-plan rule now tied to ADR 222 decision 4 plan staleness fixed control in adr_check, design and requirement text.
- R2.4 (missing scenario): The spec has no scenario for an approval default saved while a job is in approval wait that removes or relaxes the requirement; ADR 224 decision 3 says a recheck only adds, so the change must state that removal does not release the wait only through the recheck rule, which is not written. Resolution: answered: Added recheck-only-adds requirement with scenario that removal of a default while in wait does not release the wait.
- R2.5 (failure mode): Two concurrent approvals or a concurrent default save during the apply recheck can race; no requirement serializes the evaluation with the state write or the dispatch. Resolution: answered: Added serialized-recheck requirement that holds the dispatch lock across evaluation and apply dispatch.
- R2.6 (missing scenario): No scenario covers a Pulumi or OpenTofu job whose plan is evaluated with fact unavailable (available false) under an approval default; ADR 226 decision 3 says built-in gates fail closed, which the spec does not state. Resolution: answered: Added unavailable-facts-fail-closed requirement with scenario for available false.
- R2.7 (scope): Tasks 2.2 and 1.10 cover decision record and authorization, but the e2e task 3.1 covers only the wait path; the stale-plan and deny paths have no e2e step although the proposal user path names the 409 error. Resolution: answered: Extended e2e task to cover wait-resume plus deny refusal and stale-plan refusal paths.
- R2.8 (failure mode): A break-glass session under ADR 224 decision 17 must meet the gates, but the spec has no scenario for a break-glass principal starting an apply under an approval default. Resolution: answered: Added break-glass-meets-gates scenario that a break-glass apply still waits under an approval default.
- M.26 (duplicate): 'Distinct approvers', 'Stale plan refused' and 'Break-glass meets gates' duplicate several-approval-requirements-one-change and policy-approvals. The dependencies are issue IDs only. Resolution: answered: Removed Distinct approvers, Stale plan refused and Break-glass meets gates requirements and their tests; kept live wait and resume on both tools and added depends_on add-job-service-routes-start, several-approval-requirements-one-change and gate-evaluation-must-apply-dev with duplicates in out_of_scope.

## Open questions

none

Questions considered and answered:

- Does any live job path already evaluate approval defaults? No. Issue #1157 states slice 1 stored approval defaults and approval rules and pkg/gate/jobgate is tested but wired nowhere because no job service runs; the rg proof shows job.Decider is implemented in pkg/gate/jobgate/jobgate.go with zero uses of jobgate.Decider outside it.
- Which decision point must a running job evaluate? ADR 224 decisions 2 and 3 and ADR 222 decision 2 fix the points; this change evaluates at change.propose, change.planned and change.apply through the job service points that #899 and #968 provide, and leaves state.write, profile.save and profile.open to their own changes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
