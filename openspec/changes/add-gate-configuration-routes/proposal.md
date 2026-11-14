Issue: #1003, #1034, #1035, #1159.

## Why

**Persona: namespace administrator.** A namespace administrator sets who may change a stack or workspace and who must approve. On main, the gate configuration service exists and is wired, but the routes do not cover every scope for gates, approval rules and approval defaults. Policy stays out of reach for collections and units.

The user path this change must make work:

1. **Docs page.** Open the gate configuration reference page and copy the route of the target scope.
2. **API.** List, read, save and delete gates and approval rules at the scope. Read and save the approval default and the signature carry-over setting.
3. **CLI.** Run the same calls through the client that is generated from the API description.
4. **E2e.** Run the suite against the real binary to prove the routes, the 404 on another scope and the reuse of a unit name.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A principal without the read or manage action calls a gate route. | 403 with code gate.denied and the decision record ID. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates` |
| A read names an object of another scope or a missing ID. | 404 with code gate.not-found. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates` |
| A write carries a stale version in If-Match. | 409 with code gate.version-conflict. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates/deny-prod` |
| A save exceeds a scope limit. | 422 with code gate.limit. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates` |

Evidence for the claims above:

- The gate domain package exists. Proof: Pack path pkg/gate exists: true.
- The reference docs directory exists. Proof: Pack path docs/reference exists: true.
- A ConfigService implementation is wired in the SDK. Proof: rg gate.ConfigService gives pkg/dustpan/gateconfig.go:21 and internal/api/server/gate_config.go:24.
- The conformance suite names Settings.DeleteApprovalDefault. Proof: rg gives pkg/gate/gatetest/gatetest.go:634.
- No MinApprovals remains in non-test code on main. Proof: rg -n -F --glob '!*_test.go' 'MinApprovals' pkg internal cmd gives 0 matches.

## What Changes

- Gate, approval-rule and approval-default routes at installation, namespace, collection and by-name unit scopes, with read and manage actions
- The signature carry-over setting route at each scope that ADR 230 names
- Gate rows of a unit keyed on the stable unit ID
- Fresh authentication, stable error codes, audit entries and limits on each change
- A reference page in docs/reference

Out of scope:

- The DELETE route of the approval default, until the owner decides it
- Access limit routes (own change)
- Ownership rule routes (own change)
- Path set routes (own change)
- Decision record read route (own change)
- Decision points on tool paths
- The gate simulate route
- Profile draft and access request workflows
- The console policy pages
- The default collection and catch-all placement rule at namespace create (namespace create change)
- Removal of MinApprovals (no match remains on main)

Depends on:

- #996
- #1015

## Usability

Where the path still costs the person time or surprises them:

- The docs page gives one copy-paste call for each scope.
- A stale version error names the read call that gets the current version.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 3 (changed), 4 (extended), 5 (current), 6 (current).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "In the server, through the API (owner). Gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting are stored in the server."
- Quote: "A scope holds at most 256 gates, 64 access limits, 64 approval rules, 256 ownership rules and 64 path sets."
- Effect: The change serves gates, approval rules, approval defaults and the carry-over setting through the routes, actions, errors, audit entries and limits of these decisions.
- Conflict: none.

**ADR 355: Gate configuration paths and errors as shipped** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The unit scope of the gate routes is `/api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}`. The segment `by-name` is part of the path."
- Quote: "A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`."
- Effect: Every route, the docs page and the tests use the by-name unit path.
- Conflict: none.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "Approval defaults (owner). An approval default can be set on a namespace, a collection or a unit."
- Effect: The routes store approval defaults. Requirement evaluation is not changed here.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The reference page ships in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The change is not spec-ready: the Open questions section states a failed code check (command `opendustpan gates` does not exist), so the spec-ready gate refuses implementation until the proof is corrected. Resolution: answered: The errors table now uses only curl calls, not a missing command.
- R1.2 (adr conflict): ADR 230 decision 3 (changed by ADR 355) gives the unit scope as `.../units/{tool}/{name}`, but the gate-configuration spec and proposal use the by-name path only in part; the change does not state which path the docs page and the routes in tasks use, leaving the route table ambiguous against ADR 355 decision 1. Resolution: answered: All routes use the by-name path of ADR 355.
- R1.3 (owner decision conflict): Issue #1003 states the DELETE approval-default route is undecided and must be left out until decided; the change adds a Reset approval default requirement with DELETE returning 204, taking a decision the input says is open. ADR 268 and ADR 355 are cited for it, but the open-question list also says the route is answered in favor of inclusion without owner decision. Resolution: answered: The DELETE approval-default route is out of scope until the owner decides, as issue 1003 says; the ADR 268 quote is removed from the ADR check.
- R1.4 (missing scenario): The stale-write path is specified only in the proposal error table; the gate-configuration spec has no scenario for If-Match mismatch (409 gate.version-conflict), no scenario for a write without If-Match, and no scenario for a write from a token without fresh authentication where ADR 225 decision 5 requires it for destroy approvals. Resolution: answered: Scenarios Stale version, Retry, No If-Match and Access token are in the spec.
- R1.5 (missing scenario): No scenario covers the namespace create default collection and catch-all rule required by the owner decision of 13 Nov 2026 (queue P5); the change neither implements nor excludes it, although the proposal's Why and design touch the namespace scope. Resolution: answered: Out of scope list names the namespace create default; no scenario is needed in this change.
- R1.6 (failure mode): The stable-unit-ID requirement has no scenario for a unit that is renamed or restored, nor for a gate row whose unit ID is missing at migration time; the spec only tests delete-then-reuse, while the risk list names reused names reattaching deleted-unit policy. Resolution: answered: Rename and Restore scenarios added.
- R1.7 (missing scenario): The spec has no scenario for the 403 gate.denied answer with the decision record ID, nor for a write without the manage action of the addressed scope; the hostile test in design.md is not mapped to a spec scenario. Resolution: answered: Scenario No action covers the 403.
- R1.8 (failure mode): Concurrent writes to one gate row are not covered: two managers with the same If-Match version and the audit entry written in the same transaction are not specified as a failure case; the audit requirement lacks an atomicity scenario. Resolution: answered: Stale and atomic audit scenarios added.
- R1.9 (open question): The approval-requirements spec does not state how requirements are read when a rule and the scope default both apply, or when two rules name distinct groups; ADR 225 decision 3 says each rule gives one requirement, which the scenarios do not cover. Resolution: answered: Requirement evaluation moved out of this change; the out-of-scope list says so.
- R1.10 (scope): Tasks 3.1 requires the reference page, but the change also lists installation, namespace and collection routes with no task for the console review view named in the Why; the console is out of scope, yet the user path step 4 requires it. Resolution: answered: Console step removed from the user path and listed out of scope.
- R1.11 (wrong claim): The ADR check cites ADR 73 decision 4 as permitting deletion of MinApprovals with no conversion, but ADR 73 decision 4 forbids compatibility code for unreleased shapes and does not name MinApprovals; the claim is only inferred from that rule. Resolution: answered: The ADR 73 claim is removed from the ADR check.
- R2.1 (adr conflict): specs/gate-configuration/spec.md contradicts ADR 230 decision 3 and ADR 355 decision 1 by using the by-name unit path only partly; the spec's unit scope is not stated and tasks do not fix the route, so the route table is ambiguous. Resolution: answered: Scenario Unit path and the by-name decision in the design fix the unit route.
- R2.2 (open question): The DELETE approval-default route is taken as decided by ADR 268, but issue #1003 says the question is not decided and the route must be left out until the owner decides. The change adds a Reset requirement and a 204 scenario without an owner decision. Resolution: answered: The DELETE approval-default route is out of scope; the Reset requirement is removed.
- R2.3 (owner decision conflict): The owner decision of 13 Nov 2026 (queue P5) requires the default collection and catch-all rule at namespace create. The change marks it out of scope and gives no owner-approved answer for the namespace create path, so the namespace scope of the routes is left without its default placement state. Resolution: answered: Owner decision P5 is cited in considered. Namespace create belongs to its own change.
- R2.4 (failure mode): Concurrent writes with the same If-Match version and the audit write in one transaction are specified only by a one-line scenario. No requirement covers a version check that is racy at the database level, nor a retry after gate.version-conflict. Resolution: answered: Scenario Retry and the in-transaction version check cover the race.
- R2.5 (missing scenario): No scenario covers a write without If-Match, or a write without fresh authentication where ADR 225 decision 5 requires it for destroy approvals. The design lists this hostile test, but the spec has no scenario mapped to it. Resolution: answered: Scenarios No If-Match and Access token exist in the Stale write and Fresh authentication requirements.
- R2.6 (missing scenario): The stable-ID requirement has no scenario for a gate row whose unit ID is missing at migration time, or for a restore into a namespace where the unit ID changed, although the risks list names a reused name reattaching a deleted unit's policy. Resolution: answered: Scenario Restore is in the Stable unit key requirement.
- R2.7 (wrong claim): The ADR check says ADR 268 changes ADR 230 decision 3 and that the owner-tagged status of ADR 230 covers decision 3, yet ADR 230 decision 3 is not owner-tagged in the input; the owner-tag claim is inconsistent with the decision text. Resolution: answered: The ADR check no longer claims ADR 230 decision 3 is owner-tagged.
- R2.8 (open question): The open questions section states the spec is not ready and that several code checks fail, yet the change is handed on. The gate for implementation is therefore not met; the change cannot be built until the missing scenarios and the route decision are settled. Resolution: answered: The route question is settled by leaving the route out, so no open question remains.
- R2.9 (scope): The profile-gates spec adds the profile.save and profile.open points, but the proposal puts decision points on tool paths out of scope and does not address the profile draft and access request workflows that issue #1035 says must be read first from #1015. Resolution: answered: The profile-gates spec is removed; profile evaluation is out of scope.
- R2.10 (failure mode): The profile-gates scenario covers only the refused save. No scenario covers a refused open, a gate in error (gate.ErrRuleFailed, which must fail closed under ADR 224 decision 13), or a restart in the middle of a save. Resolution: answered: The profile-gates spec is removed with it.
- R2.11 (missing scenario): The approval-requirements spec has no scenario for distinct people across requirements (ADR 225 decision 6) and no scenario for a rule with a timeout or an approver of a wider scope (ADR 225 decision 1). Resolution: answered: The approval-requirements spec is removed; requirement evaluation is another item.
- R2.12 (open question): The signatureCarryOver setting and the approval defaults with freshAuthSeconds are listed in scope but have no requirement or scenario in any spec, so their behavior on write and read is not defined. Resolution: answered: Requirement Carry-over setting and scenario Read default added.
- M.22 (duplicate): The access-limit, ownership-rule, path-set and decision-record routes duplicate add-access-limits-per-scope, rest-routes-ownership-rules-create, add-named-path-sets-paths and read-route-decision-records-change. Resolution: answered: Access limit, ownership rule, path set and decision record routes are removed from the scope and listed as out of scope. Only gate, approval-rule, approval-default and carry-over routes stay.

## Open questions

none

Questions considered and answered:

- Are gate objects stored in the server? Yes. ADR 230 decision 1 and ADR 222 decision 6.
- Is the DELETE route of the approval default in scope? No. Issue 1003 says it is not decided and leaves it out until the owner decides.
- Which key names the unit scope of gate rows? The stable unit ID, by the owner decision of 19 October 2026 in the issue. The path keeps the by-name form of ADR 355 decision 1.
- Is the default collection at namespace create part of this change? No. Owner decision, 13 Nov 2026 (queue P5) puts it in the namespace create transaction. These routes let an administrator list and delete such gates later.
- Do access limits, ownership rules, path sets and decision records belong here? No. Other changes of this milestone own them.
- Does MinApprovals need removal work? No. The rg of non-test code gives 0 matches.
- How is a race on If-Match handled? The store compares the version inside the write transaction, so only one writer wins.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
