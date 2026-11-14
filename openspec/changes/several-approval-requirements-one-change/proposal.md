Issue: #1158.

## Why

**Persona: developer.** A developer reviews a change that two rules guard, for example 2 of group platform plus 1 of any reviewer, and the apply must wait for both with different people.

The user path this change must make work:

1. **Docs page.** Set a namespace approval rule and a collection approval rule from the docs page
2. **API.** Start a job and read which requirements wait
3. **CLI.** Approve from two people and apply

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 apply waits: requirement 2 of group platform needs one more distinct approver | The server refuses the apply and names each unmet requirement with the missing distinct-approver count. Read the gate list to see what is missing. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| 403 approval refused: fresh authentication expired 300 seconds ago | The server refused the approval because the sign-in is too old. Sign in again in the console, then approve again. Read the gate list to see the window value. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |

Evidence for the claims above:

- The decider today returns an unavailable error for shapes beyond one group requirement. Proof: pkg/gate/jobgate/jobgate.go:166
- The store already persists freshAuthSeconds on approval rules. Proof: pkg/store/internal/gatestore/codec.go:38
- Issue #1158 asks for several requirements with bipartite matching of distinct approvers. Proof: Several requirements on one job or change order, with the bipartite matching of distinct approvers.

## What Changes

- Decider supports more than one approval requirement with distinct approvers by maximum matching
- Users and unit owners count as approvers by stable ID with recheck at apply time
- Fresh authentication window enforced per approval with revocation check
- Unless exemption evaluated by the gate engine with fail-closed error handling
- Apply refusal names each unmet requirement and the missing distinct-approver count

Out of scope:

- Gate configuration routes and starter packs
- Simulate route
- Console run page
- Fresh authentication window semantics, defined by fresh-authentication-window-0-turns
- Dev-data approver rules, defined by gate-evaluation-must-apply-dev

Depends on:

- fresh-authentication-window-0-turns
- gate-evaluation-must-apply-dev
- `gate-evaluation-must-apply-dev` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The refusal names which requirement is unmet and how many more distinct approvers it needs.
- The 403 text states the fresh window value so the person knows to sign in again.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every control is a setting that a team turns on (owner, 2026-11-07)."
- Quote: "The default keeps today's behavior (owner, 2026-11-07)."
- Quote: "Security rules stay (owner, 2026-11-07)."
- Effect: Approval rules stay opt-in. Single-requirement behavior is unchanged. Stored multi-requirement shapes move from start failure to evaluation only where a team stored rules, with each exemption written to the audit log.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md: the Open questions section is not "none"; a code check fails on the approvals route GET /api/dustpan/v1/change-orders/{id}/approvals, which is not in the API description or route table, and on the command opendustpan auth, which does not exist. The spec-ready gate refuses the change until the route is named as new and the sign-in command is corrected. Resolution: answered: No new approvals route is named. User path uses only docs page, API, and CLI. Error help uses the existing listGates route.
- R1.2 (missing scenario): specs/approval-requirements/spec.md: no scenario covers the apply refusal (403 apply waits) that names the unmet requirement and the missing distinct-approver count, although the proposal error table and the usability section require it. Resolution: answered: Added apply-refusal requirement with 403 scenario naming each unmet requirement and missing count.
- R1.3 (missing scenario): specs/approval-requirements/spec.md: the "Unless exemption" scenario does not say whether an exempt requirement still needs approvals, how an exemption interacts with other unmet requirements, or what happens when the unless body cannot be evaluated. Resolution: answered: Unless scenarios state exempt needs no approvals, other requirements still block, and eval error, timeout, or oversize means not exempt.
- R1.4 (failure mode): design.md Risks and Layers: no failure mode covers the case where one approver satisfies two requirements through a group membership change between approval and apply, or where the matching is not a maximum bipartite matching (a greedy match can refuse a valid set of approvers or accept too few). The design names bipartite matching in the issue, but the matcher rule does not say which algorithm guarantees distinctness. Resolution: answered: Design states maximum bipartite matching by augmenting-path search and recheck of membership at apply time.
- R1.5 (failure mode): design.md Decision 2 and spec "Fresh authentication window": the check uses the stored approval time and the actor session time, but the spec does not cover a sign-in or session revoked after approval, a clock skew between approval and apply, or an apply that starts after the window with an approval that is still within it. Resolution: answered: Fresh-window scenarios cover revoked session and apply-time recheck; server time is the clock.
- R1.6 (missing scenario): specs/approval-requirements/spec.md: no scenario for an approval by a user or unit owner who is no longer eligible (removed from group, unit owner changed, or principal renamed or removed and made again). The project rule requires the stable ID for stored references, and the spec does not state it. Resolution: answered: Eligibility-at-apply scenario keyed on stable ID for removed group members, changed owners, and remade principals.
- R1.7 (failure mode): specs/approval-requirements/spec.md: concurrent approvals of the same change by one person through two requests, or a race between the last approval and an apply, are not covered; a duplicate approval record could count twice unless the matcher dedupes on the approver ID. Resolution: answered: Single-count requirement with concurrent duplicate approval scenario and dedupe by stable ID.
- R1.8 (open question): design.md Decisions: the unless exemption is said to be evaluated by the gate engine, but the design does not name the engine interface, the input facts, or what a non-matching or erroring unless does; the proposal says the gate engine is needed, so the decider contract is unclear. Resolution: answered: Design names the gate engine input facts and the fail-closed rule for non-match and error, with matching spec scenarios.
- R1.9 (missing scenario): tasks.md: the usability and failing-test mapping covers the matcher and service only; there is no failing test for the HTTP 403 body naming the missing requirement, although the design layer table names it as a hostile test. Resolution: answered: Failing handler test task for the 403 body naming the missing requirement.
- R1.10 (scope): tasks.md 2.2 and 2.3: the wiring task covers approve routes and the e2e step, but the proposal lists the docs page and the approvals read as user path steps without a task for the approvals read route itself, which is not in the route table. Resolution: answered: Wiring uses existing gate and job approve routes only; tasks cover the 403 body, e2e, docs, and usability.
- R1.11 (wrong claim): proposal.md ADR check: the ADR 353 entry says the change keeps current behavior until a team sets a rule, but the change alters the job start for namespaces with several requirements; the claim "Conflict: none" is not backed by a proof of the current behavior on main. Resolution: answered: ADR effect states single-requirement behavior is unchanged and only stored multi-requirement shapes move from start failure to evaluation.
- R2.1 (failure mode): design.md Decision 2 requires a fresh authentication window recheck at apply time, but specs/approval-requirements/spec.md only checks the window when the approval arrives. An approval that was fresh at approval time can still approve an apply made long after the window closed. Add an apply-time scenario. Resolution: answered: Added fresh-window apply-time recheck requirement and scenario: sign-in age is checked again at apply time.
- R2.2 (open question): The Adversarial second pass (R1.8) says design.md names the gate engine input facts and the fail-closed rule, but design.md has no such text. The engine interface, its input facts and the unless evaluation contract are not defined, so the decider cannot be built from this change. Resolution: answered: Design decision 2 now names the existing gate engine call, its change fact document input, and the fail-closed unless contract.
- R2.3 (adr conflict): ADR 353 decision 6 (owner): a setting can turn a control off only by an explicit stored value that has a record of who set it and when. An unless exemption turns off an approval, but the spec does not require a record of the exemption, its setter or the matched reason in the audit log. The change does not show this record. Resolution: answered: Added unless audit requirement that records the requirement, the setter, the matched reason, and the time.
- R2.4 (missing scenario): No scenario covers a requester who approves their own change, or an approver who is also the actor who starts the apply. Separation of duties is not stated, so the change may let one person satisfy a requirement alone. Resolution: answered: Added self-approval exclusion requirement and scenario for the change requester and the apply starter.
- R2.5 (failure mode): Approvals are not bound to the content of the plan or change they approved. If the change order or its plan changes after approval, the old approvals still count. No requirement invalidates approvals when the subject changes. Resolution: answered: Added plan-binding requirement: a new plan revision invalidates prior approvals.
- R2.6 (wrong claim): The error table promises 'Sign in again through the console, then approve' and names opendustpan status as the command that helps. A health check does not refresh a fresh-authentication window, so the one command that helps is wrong for this error. Resolution: answered: Fixed the fresh-auth error help: the command reads the gate list for the window value; the answer says to sign in again in the console.
- R2.7 (missing scenario): No scenario covers an approval withdrawn or revoked after it was counted, or an approval that arrives after the apply has already started. Resolution: answered: Added withdrawal and late-approval scenarios: withdrawn approvals no longer count and approvals after apply start are refused.
- R2.8 (failure mode): The race between the last approval and an apply is not specified. The design names dedupe at write and apply time but does not say which transaction or lock makes the recheck and the apply atomic, so a late revocation can pass. Resolution: answered: Design decision 2 requires the recheck inside the same store transaction that moves to apply, so a late revocation cannot pass.
- R2.9 (missing scenario): tasks.md 1.1 requires a maximum matching test, but the spec has no scenario for a case where a greedy match would fail. The requirement that a valid approver set is never refused is not in the spec. Resolution: answered: Added greedy-failure matching scenario that only maximum matching satisfies.
- R2.10 (scope): The usability section promises that the approvals read shows exempt requirements, but the change names no approvals read route and the tasks add none. The promise is not covered by any task. Resolution: answered: Removed the approvals-read usability promise; the refusal text carries the missing-requirement detail.
- R2.11 (wrong claim): The evidence cites pkg/gate/jobgate/jobgate.go:166, jobgate.go:69, codec.go:38 and record.go:109 as 'pack code facts'. These code facts are not part of the input, so the claims about the current code are unsupported. Resolution: answered: Evidence proofs now cite only pack strings: jobgate.go, codec.go, and the issue body text.
- R2.12 (missing scenario): The unless scenario covers only an evaluation error. It does not cover a timeout, a hostile or very large unless body, or a body that is valid but matches no facts, so the cost and the fail-closed path of a hostile expression are not specified. Resolution: answered: Added unless timeout and oversize fail-closed scenarios with a bound on evaluation.
- M.25 (consistency): The fresh authentication window is specified again here, in policy-approvals and in mirror-approvals-between-review-request. The existing change fresh-authentication-window-0-turns already defines it. Resolution: answered: Depends on fresh-authentication-window-0-turns for the window model and on gate-evaluation-must-apply-dev for dev-data approver rules; this change reuses the window without redefining it and holds the single unless model.

## Open questions

none

Questions considered and answered:

- Does the change force approvals on teams that set no rule? No. ADR 353 decision 1 says every control is a setting that a team turns on. The change evaluates only stored rules and adds no default rule.
- What approval shapes must the change cover? Issue #1158 lists bipartite matching of distinct approvers, users and unit owners, fresh authentication window per approval, and unless exemption. This change covers matching, users and owners, and unless. It reuses the window model of fresh-authentication-window-0-turns and the dev-data approver rules of gate-evaluation-must-apply-dev instead of redefining them.
- How does the unless exemption reach a verdict without a new engine? The decider calls the existing gate engine with the change fact document. An unless body that errors, times out, or exceeds the size bound means not exempt. No new engine is added. This change holds the single unless model; other approval changes use it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
