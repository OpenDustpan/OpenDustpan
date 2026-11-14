Roadmap: `docs/roadmap.md:280` (Policy and approvals).

## Why

**Persona: namespace administrator.** A namespace administrator needs to require two distinct people before an apply runs on runners. The administrator runs the composed step-6 path through runner jobs and the server holds the apply in awaiting-approval, resets on a new plan, refuses a destroy approval without a fresh step, refuses a forbid limit with its name in the record and refuses a plan that an enforce gate does not allow.

The user path this change must make work:

1. **API.** Set the approval, gate and limit through the owning pages and start plan and apply jobs for a stack and a workspace
2. **CLI.** Run the plan and apply with the real CLI
3. **Console.** Approve once, see awaiting-approval, complete the fresh destroy step where needed, see apply start
4. **Docs page.** Follow the docs page for the destroy fresh step and the composed run
5. **E2e.** Run e2e step on SQLite and PostgreSQL through real binary over TLS

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| destroy approval without fresh authentication | 403 mfa.fresh-step-required, change nothing, sign in again for a fresh step | `opendustpan approve --help` |
| apply refused by access limit | 403 with limit ID, decision record names policy, change nothing | `opendustpan status --help` |
| approval on old digest or stale plan | 409 with stale units or old digest, attach a new revision | `opendustpan check --help` |

Evidence for the claims above:

- Approval rule e2e exists Proof: e2e/approval_rules_test.go
- State write gate e2e exists Proof: e2e/state_write_gate_test.go
- Approval rule guide exists Proof: docs/guides/approval-rules.md
- State write gate guide exists Proof: docs/guides/state-write-gates.md
- No MFA e2e test names MFA Proof: MFA keyword evidence lists e2e_count 0
- MFA code exists Proof: pkg/mfa

## What Changes

- Destroy approval needs a fresh step, with 403 for tokens and for persons with no enrolled factor
- Composed step-6 run through runner jobs for a stack and a workspace that exercises the owning two-person wait, plan reset, limit refusal with decision record and enforce gate refusal

Out of scope:

- Two distinct approvers, approval defaults, ownership rules and signature carry-over, owned by several-approval-requirements-one-change and approval-defaults-take-effect-live
- Access limit scopes, expiry and routes, owned by add-access-limits-per-scope
- Review-request approval mirror, owned by mirror-approvals-between-review-request
- Fresh-authentication windows and factors, owned by mfa and fresh-authentication-window-0-turns
- Runner join, claim and log streaming
- Planner role alone
- Pull request automation and comments
- Apply after merge and pre-merge apply option
- Write-back commits
- CLI-driven remote execution
- Events and notifications
- Output sharing reads
- Run triggers and schedules
- Console run page

Depends on:

- add-access-limits-per-scope
- approval-defaults-take-effect-live
- fresh-authentication-window-0-turns
- mfa
- mirror-approvals-between-review-request
- planner-role-2
- runners
- several-approval-requirements-one-change
- `runners` (order of the milestone review)
- `planner-role-2` (order of the milestone review)
- `approval-defaults-take-effect-live` (order of the milestone review)
- `several-approval-requirements-one-change` (order of the milestone review)
- `add-access-limits-per-scope` (order of the milestone review)
- `evaluate-gates-on-pulumi-jobs` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Destroy fresh step surprises token users; refusal must name the sign-in path for a fresh step
- Two-person wait costs time on small teams; the run view must name who already approved and what stays missing

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 5).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "Fresh MFA for approvals (owner). An approval of a change that holds a `destroy` needs a fresh authentication, always."
- Effect: This change owns only the destroy fresh check. Counts, defaults, ownership and carry-over stay with the owning changes.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current), 5 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Destroy signature uses the one window. Token never gives a fresh step and gets the 403 error. No window logic is redefined here.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Effect: Destroy approval is a change that needs a fresh step. The error text itself follows ADR 227.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A factor of OpenDustpan gives the fresh step (owner)."
- Quote: "No factor, no fresh step (owner)."
- Effect: OAuth and trusted-header users give the fresh step with an enrolled factor. A user with no factor gets 403 that names the enrollment path.
- Conflict: none.

**ADR 191: Plan revisions, unit plans and signatures** (owner-tagged: decision 6).

- Decisions that apply: 5 (changed), 6 (changed), 7 (current).
- Changed by: ADR 222 changes decision 6; ADR 225 changes decision 6; ADR 227 extends; ADR 299 changes decisions 4, 5 and 6; ADR 300 extends. Chain to the end: ADR 222, ADR 225, ADR 227, ADR 299, ADR 300, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 222, 225, 227, 299, 300.
- Quote: "A new plan resets the signatures (owner). An attach voids each signature of each earlier revision, in the same transaction."
- Quote: "A signature (`changeorder.Signature`) is the record that binds one approval to one revision."
- Effect: Reset, digest and staleness stay with the owning changes. This change only composes them in the step-6 run.
- Conflict: none.

**ADR 223: Access limits: Cedar forbid policies for each scope** (not owner-tagged).

- Decisions that apply: 3 (current), 8 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "After the grants. The `identity.Authorizer` checks the grants first."
- Quote: "A refusal writes a decision record. An access limit that refuses a call writes a decision record (ADR 229)."
- Effect: Limit refusal and its decision record stay with the access-limits change. This change only composes the refusal in the step-6 run.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 14).

- Decisions that apply: 7 (current), 14 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Modes. A gate in `enforce` mode gives its outcome."
- Quote: "Combining (owner). For each unit, in this order: 1. Any `deny` gives `deny`, with the message of each `deny`."
- Effect: Enforce and evaluate behavior and combining stay with the owning changes. This change only composes the outcomes in the step-6 run.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md Open questions section reads 'Not spec-ready' with failing code checks (commands opendustpan auth, gates and change-order do not exist); the change may not be implemented until this section reads none, so the change cannot be built as written. Resolution: answered: Open questions reads none; considered answers each prior question.
- R1.2 (adr conflict): specs/policy-approvals/spec.md 'Destroy needs fresh step' scenario requires a fresh step only for approvals covering a destroy; ADR 227 decision 5 and ADR 225 decision 5 also require it for approval rules with freshAuthSeconds and for namespaces with freshAuthForApprovals, which the spec omits. Resolution: answered: Rule and namespace windows stay with the owning changes; this change uses the shortest window that holds.
- R1.3 (missing scenario): No scenario covers the access-token refusal of the destroy approval (ADR 227 decision 6: an access token never gives a fresh authentication), though design.md and tasks.md name it; the spec must state the 403 for token callers explicitly. Resolution: answered: Token-never-fresh kept as the owned destroy check.
- R1.4 (missing scenario): No requirement or scenario for the Two distinct approvals case where the same subject approves twice or replays an old digest; design.md risks name this bypass but the spec only covers one approval and a second distinct approval. Resolution: answered: Distinct-approver count stays with several-approval-requirements-one-change; this change composes only.
- R1.5 (missing scenario): Stale plan refusal (ADR 191 decision 7, changeorder.ErrPlanStale naming stale units) and the 409 ErrPlanChanged for a digest that is not current have no scenario in the spec, though proposal and design require them. Resolution: answered: Stale and digest behavior stays with the owning changes; this change composes only.
- R1.6 (missing scenario): The apply refusal by an access limit and the enforce gate deny have no scenario for break-glass sessions (ADR 223 decision 7 and ADR 224 decision 17) or for evaluate-mode gates, which must record but not refuse. Resolution: answered: Break-glass and evaluate behavior stay with the owning changes; this change composes only.
- R1.7 (failure mode): Recheck at change.apply (ADR 224 decision 3) can add a new deny or a new requirement after approval; the spec and design do not say what happens to signatures already given and whether apply then waits again. Resolution: answered: Recheck-adds stays with the owning changes; this change composes only.
- R1.8 (failure mode): Error path when the Cedar engine fails during limit or gate evaluation (ADR 223 decision 5, ADR 224 decision 13): fail-closed with gate.ErrRuleFailed is not stated in the spec, so an engine error could be read as allow. Resolution: answered: Engine-error fail-closed stays with the owning changes; this change composes only.
- R1.9 (open question): The proposal states the approval rule resolves at scope with count two, but no ADR or spec says how the rule is set on the unit (approval default or gate outcome) when both exist; the change takes this choice silently in design decision 1. Resolution: answered: Combining stays with the owning changes; this change adds no requirement source.
- R1.10 (wrong claim): The ADR check for ADR 191 cites decision 6 as the only owner rule; the proposal's claim that the fresh step is the ADR 212 decision 3 window of ADR 227 is accurate, but the proposal names ADR 212 as owner-tagged while the ADR 212 entry lists owner decision 3 with a 'Current rule' that omits ADR 301 decision 1 factor rules for OAuth sign-in users who have no factor (ADR 301 decision 4), which can refuse the destroy approval for a person with no enrolled factor. Resolution: answered: ADR 301 entry kept with exact quotes for the owned destroy check.
- R2.1 (owner decision conflict): specs/policy-approvals/spec.md requires the destroy fresh step only for approvals covering a destroy; the owner decision in ADR 301 decision 4 and ADR 227 decision 6 require refusal with mfa.fresh-step-required for a person with no enrolled factor, and the spec has no scenario for that case. Resolution: answered: Added no-enrolled-factor scenario to destroy-fresh requirement with 403 and no change.
- R2.2 (adr conflict): design.md decision 3 says a person with no enrolled factor gets 403, but the spec scenarios never check that the refusal names the enrollment path, which ADR 301 decision 4 requires for the error text. Resolution: answered: No-factor scenario now requires the refusal text to name the enrollment page and each way to enroll the first factor per ADR 301 decision 4.
- R2.3 (missing scenario): No scenario covers an approval whose digest matches but whose signature the store rejects as a replay of a voided signature after an attach; the void-on-attach requirement has no test for a replayed old-revision signature. Resolution: answered: Replay-voided-signature scenario kept with the owning changes; this change composes only.
- R2.4 (open question): ADR 225 decision 12 (signatureCarryOver, owner 2026-10-18) can change whether a new plan voids signatures; the change does not say whether the setting is off by default in this change, and the spec's void requirement states no exception. Resolution: answered: signatureCarryOver stays with the owning changes; this change adds no carry-over rule.
- R2.5 (failure mode): Concurrent approvals from two distinct subjects on the same digest race in the store; the design names a distinct-subject check but not the transaction or lock that makes the count of two atomic. Resolution: answered: Atomic count stays with several-approval-requirements-one-change; this change composes only.
- R2.6 (missing scenario): Break-glass sessions are covered for limits, but no scenario says what a break-glass session does with the destroy fresh step or with approval counts, although ADR 224 decision 17 says approval rules hold for such changes. Resolution: answered: Break-glass destroy fresh rule kept as part of the owned destroy check; counts stay with the owning change.
- R2.7 (failure mode): A restart between the decision record write and the refusal response is not covered; the limit refusal could leave an apply refused with no record, which the usability note calls out. Resolution: answered: Same-transaction record stays with add-access-limits-per-scope; this change composes only.
- R2.8 (wrong claim): proposal.md cites ADR 212 decision 3 as the source of the 403 mfa.fresh-step-required error, but that error is defined in ADR 227 decision 6; the ADR 212 entry should carry ADR 227 as the error source. Resolution: answered: ADR 212 effect names ADR 227 decision 6 as the source of 403 mfa.fresh-step-required.
- R2.9 (scope): tasks.md item 2.1 names GET .../gates as the check for wiring of job start and approve, which does not test approve or apply routes; the acceptance check does not cover the wiring it claims. Resolution: answered: Wiring task exercises the composed path and the owned 403 and 409 cases.
- R2.10 (open question): The spec does not state how a stale-plan refusal interacts with a pending approval: whether the signatures are voided or kept when apply is refused as stale, as ADR 191 decision 7 says a stale plan does not void a signature. Resolution: answered: Stale-plan void behavior stays with the owning change; this change composes only.
- M.3 (duplicate): Distinct approvers, the plan reset, the recheck at apply, break-glass, evaluate mode, fresh windows and the access-limit refusal are also in approval-defaults-take-effect-live, several-approval-requirements-one-change, mirror-approvals-between-review-request and add-access-limits-per-scope. Resolution: answered: Trimmed to the owned destroy fresh check plus one composed step-6 run; counts, resets, rechecks, break-glass counts, evaluate mode, windows and limit refusals moved to depends_on and out_of_scope with the owning changes.

## Open questions

none

Questions considered and answered:

- Who owns two distinct approvers and the plan reset? This change keeps Destroy needs fresh step. Counts of two, defaults, carry-over, void, digest and staleness stay with several-approval-requirements-one-change and approval-defaults-take-effect-live.
- Does destroy approval need a fresh step for token callers? ADR 225 decision 5 with ADR 227 and ADR 301: destroy approval always needs a fresh authentication; tokens never give it; a person with no enrolled factor gets 403 that names the enrollment path. Windows themselves stay with mfa and fresh-authentication-window-0-turns.
- Who owns limit refusals and enforce gate refusals? ADR 223 with ADR 224: forbid refusal and gate outcomes stay with add-access-limits-per-scope and the gate changes. This change only runs the composed step-6 path through runner jobs.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
