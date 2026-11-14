---
version: 2
---

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
| destroy approval without fresh authentication | 403 mfa.fresh-step-required, change nothing, the text names the sign-in path for a new sign-in | `make e2e` |
| apply refused by access limit | 403 with limit ID, decision record names policy, change nothing | `make e2e` |
| approval on old digest or stale plan | 409 with stale units or old digest, attach a new revision | `make e2e` |

Evidence for the claims above:

- Approval rule e2e exists Proof: e2e/approval_rules_test.go
- State write gate e2e exists Proof: e2e/state_write_gate_test.go
- Approval rule guide exists Proof: docs/guides/approval-rules.md
- State write gate guide exists Proof: docs/guides/state-write-gates.md
- No e2e test names MFA Proof: MFA keyword evidence lists e2e_count 0
- MFA code exists Proof: pkg/mfa

## What Changes

- Destroy approval needs a fresh step, with 403 for tokens and agents and for persons with no enrolled factor
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
- evaluate-gates-on-pulumi-jobs
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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- State-write enforce helper refuses writes that gates do not allow (`pkg/gate/statewrite.go:618 (func stateWriteRun.enforce)`)
- Freshness hook test covers the fresh step path (`pkg/accesstoken/accesstoken_test.go:779 (func TestFreshnessHook)`)
- Job service test covers planner refusal of apply and destroy (`pkg/dustpan/jobservice_internal_test.go:448 (func TestJobPlannerRefusesApplyAndDestroyWithTheOldAnswer)`)
- Concurrent approvals test covers one entry and one operation (`pkg/dustpan/auditchain_approval_test.go:108 (func TestConcurrentApprovalsLeaveOneEntryAndOneOperation)`)
- Policy bounds test covers token lifetime (`pkg/accesstoken/accesstoken_test.go:1160 (func TestPolicyBoundsLifetime)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 191: Plan revisions, unit plans and signatures** (owner-tagged: decision 6).

- Decisions that apply: 5 (changed), 6 (changed), 7 (current).
- Changed by: ADR 222 changes decision 6; ADR 225 changes decision 6; ADR 227 extends; ADR 299 changes decisions 4, 5 and 6; ADR 300 extends. Chain to the end: ADR 222, ADR 225, ADR 227, ADR 299, ADR 300, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 222, 225, 227, 299, 300.
- Quote: "A signature (`changeorder.Signature`) is the record that binds one approval to one revision."
- Quote: "A new plan resets the signatures (owner). An attach voids each signature of each earlier revision, in the same transaction."
- Effect: Reset, digest and staleness stay with the owning changes. This change only composes them in the step-6 run.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Effect: Destroy approval is a change that needs a fresh step. The 403 code itself follows ADR 227 decision 6.
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
- Effect: Destroy signature uses the one window. A token never gives a fresh step and gets the 403 error. No window logic is redefined here.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A factor of OpenDustpan gives the fresh step (owner)."
- Quote: "No factor, no fresh step (owner)."
- Effect: OAuth and trusted-header users give the fresh step with an enrolled factor. A user with no factor gets 403 that names the enrollment path.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Who owns two distinct approvers and the plan reset? Counts of two, defaults, carry-over, void, digest and staleness stay with several-approval-requirements-one-change and approval-defaults-take-effect-live (ADR 191 decision 6, ADR 225).
- Does destroy approval need a fresh step for token callers? ADR 225 decision 5 with ADR 227 and ADR 301: destroy approval always needs a fresh authentication; access, job and trust tokens and agent credentials never give it; a person with no enrolled factor gets 403 that names the enrollment path.
- Who owns limit refusals and enforce gate refusals? ADR 223 with ADR 224: forbid refusal and gate outcomes stay with add-access-limits-per-scope and the gate changes. This change only runs the composed step-6 path through runner jobs.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
