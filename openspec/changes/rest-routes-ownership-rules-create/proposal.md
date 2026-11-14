Issue: #1238, #1240.

## Why

**Persona: namespace administrator.** A namespace administrator manages who must approve a change of each area of code. Today the administrator cannot read or write ownership rules through the API, and a pipeline cannot create or tighten a rule with an access token because each token write answers 403. This change gives the administrator list, read, put and delete routes with safe concurrency and lets automation tighten rules while a weakening change still needs a fresh authentication.

The user path this change must make work:

1. **Docs page.** Read the ownership rules guide and list the rules of the namespace.
2. **API.** Read one rule, edit it and put it back with If-Match; retry after a 412 with a fresh read.
3. **API.** Tighten a rule from a pipeline with an access token; sign in as a person for a weakening change.
4. **Console.** Check the audit entry for each write.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Put with a stale If-Match fails. | 412 with code precondition-failed. The rule is unchanged. Send the read again, merge the change and send the put with the new ETag. | `GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules` |
| Weakening write with an access token fails. | 403 with code mfa.fresh-step-required. The rule is unchanged. Sign in again as a person and repeat the write within the fresh window. | `opendustpan status` |
| Read of an unknown rule ID fails. | 404 with code not-found. Nothing changes. List the scope and use an ID from the list. | `GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules` |

Evidence for the claims above:

- No REST route reads or writes ownership rules today. Proof: Issue #1238: The gate package stores ownership rules (ADR 225 decision 9). No REST route reads or writes them.
- Access-token writes to gate configuration answer 403 with a plain message and no fresh-step code. Proof: Issue #1240: Every write to approval rules and approval defaults with an access token answers `403` with a plain message. It has no `mfa.fresh-step-required` code.
- The coverage matrix lists gate routes and no ownership-rule route. Proof: Coverage note: each empty or weak cell is a candidate scenario, with listed routes DELETE /api/dustpan/v1/namespaces/{namespace}/gates/{id} and PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id} and no ownership-rule path.

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules for list with paging
- New route GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules/{id} for read with ETag
- New routes PUT and DELETE /api/dustpan/v1/namespaces/{namespace}/ownership-rules/{id} with If-Match, 412 on stale match and declared authorization actions
- Matching list, read, put and delete routes for ownership rules at collection scope and at unit scope, with the same preconditions and actions on the scope object
- One shared weakens definition for approval rules, approval defaults and ownership rules with 403 mfa.fresh-step-required on weaken with an access token
- User documentation page for the new routes in the same pull request
- Route tests, service tests and an e2e step that drives each route first

Out of scope:

- Approval rule and approval default route shapes; only their weakens evaluation is shared
- Gate evaluation and decision records
- CLI and provider releases that use the new routes
- Collection update route and create-only writes, shipped as separate changes of the ADR 270 decision 5 order

Depends on:

- add-named-path-sets-paths
- `add-named-path-sets-paths` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The namespace administrator must find the correct scope path for a rule; the docs page shows one copyable example for each scope.
- A stale 412 surprises a pipeline; the error names the read command that gives the new ETag.
- A weaken refusal with an access token surprises automation; the error names the fresh sign-in path.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 2, 5, 9).

- Decisions that apply: 2 (current), 5 (current), 9 (current).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "Ownership rules (owner). An ownership rule is stored in the server and configured through the API."
- Quote: "Sources of requirements (owner, 2026-10-18). A requirement comes from one of three sources: a gate whose outcome names an approval rule (ADR 224 decision 14), an approval default (decision 4), or an ownership rule in `enforce` mode that applies (decision 9)."
- Effect: The routes expose the stored ownership rule of decision 9 as a requirement source of decision 2. Approval of a change that holds a destroy still needs a fresh authentication under decision 5. No words of an owner-tagged decision change.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 3 (extended), 6 (current), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Resource-oriented paths. A path names a collection or a resource in it."
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).**"
- Effect: The routes use resource paths under the namespace, page lists with cursor, use ETag with If-Match, follow the JSON style guide and accept Idempotency-Key on writes.
- Conflict: none.

**ADR 227: One fresh-authentication window** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3)."
- Effect: A weakening write with an access token fails closed with the stable code and changes nothing. An access token never gives a fresh authentication.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Effect: A change that reduces security needs a fresh authentication. The weakens function applies this rule to gate-configuration writes; the token rule comes from ADR 227 decision 6.
- Conflict: none.

**ADR 270: Manage OpenDustpan as code with a CLI and two providers** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 5 (current), 7 (current), 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Ownership rules are managed as code through the providers."
- Quote: "**The order of work is the CLI, then the OpenTofu provider, then the Pulumi provider.**"
- Effect: The routes are the ownership-rule route step of the owner-ordered sequence in decision 5. They let the CLI and providers manage ownership rules as code with access tokens, ETag preconditions and no adopt on create under decisions 3, 7 and 8.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The pull request adds the user documentation page for the new routes in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md: the Open questions section is not 'none'; it records failing code checks (route GET .../ownership-rules/{id} and GET .../ownership-rules are not in the API description, and command 'opendustpan auth' does not exist), so the spec-ready gate refuses the change until the error table is fixed. Resolution: answered: Open questions reads none. Error table uses only the new list path proposed in what_changes, the real server status command, and no extra failing check text.
- R1.2 (wrong claim): proposal.md: the error table names 'opendustpan auth sign-in' and ownership-rule GET routes as help commands; the command does not exist and the routes are not in the API description, so the one-command help promise of the user path is unsupported. Resolution: answered: Removed the non-existent auth command. Help commands are the proposed list route and opendustpan status, both named in what_changes and user_path.
- R1.3 (missing scenario): specs/ownership-rules/spec.md: no scenario for a put or delete with an If-Match header absent, or with a weakening write that arrives with a fresh session but after the fresh window has expired; the 403 path is only covered for access tokens, not for the expired-window person session. Resolution: answered: Added missing If-Match, expired fresh-window session and token-never-fresh scenarios.
- R1.4 (missing scenario): specs/ownership-rules/spec.md: no requirement defines what 'weakens' means for an ownership rule (fields, mode change from enforce to evaluate, path widening, count reduction, freshAuthSeconds increase). design.md says the weakens function is a pure function, but no spec scenario lists which field changes weaken, so the risk named in design.md (a missed field) is untested. Resolution: answered: Added the weakens-definition requirement with field-level weaken and tighten cases and per-direction scenarios.
- R1.5 (adr conflict): ADR 225 decision 5 and ADR 227 decision 5: an ownership rule with freshAuthSeconds or a change that holds a destroy needs a fresh step; the spec's 'Non-weakening write with access token' accepts any tighten with an access token without checking whether the new rule sets freshAuthSeconds or moves a rule into enforce mode, which ADR 212 decision 3 treats as a security-relevant change. Resolution: answered: Weakens definition covers freshAuthSeconds lengthening and enforce to evaluate as weakening; tighten with token still needs a live session and never gains fresh auth from a token. Added destroy fresh-step scenario.
- R1.6 (failure mode): design.md Layers: the port adapter row claims a version compare on the namespace fence transaction, but no spec scenario covers a delete racing a put (a put on a rule deleted concurrently, or a put that creates a rule with an existing ID in a narrower scope, ErrFinal from ADR 225 decision 1). Resolution: answered: Added race scenarios for delete racing put and narrower-scope ID reuse refused with ErrFinal.
- R1.7 (missing scenario): specs/ownership-rules/spec.md: no scenario for a put that changes the rule ID, or for a put to an unknown ID (create versus update), although ADR 270 decision 6 forbids create that adopts an existing object. Resolution: answered: Added ID immutability and create-versus-adopt scenarios under ADR 270 decision 6.
- R1.8 (failure mode): design.md Risks and Layers: a hostile input test is named for oversize body and unknown fields, but no scenario covers a path-traversal or glob-bomb in the paths field of an ownership rule, or a when-expression that exceeds the evaluator limits. Resolution: answered: Added hostile paths and when-expression limit scenarios.
- R1.9 (owner decision conflict): ADR 270 decision 8 (owner) requires If-Match on every update and delete and no server reconciliation; the spec delete requirement covers If-Match but the spec does not state the 412 answer for delete, only for put. Resolution: answered: Delete requirement now states the 412 answer on stale match per ADR 270 decision 8.
- R1.10 (scope): tasks.md 2.2 and 3.1: the task list adds the approval-rule and approval-default weakens check through the shared function but the spec and proposal place approval-rule route changes out of scope; the spec has no scenario for approval rule or approval default writes with access tokens, so those tasks have no acceptance scenario. Resolution: answered: Weakening-token requirement now covers approval rules and approval defaults with scenarios, matching the shared-function task; route-shape work on those objects stays out of scope.
- R2.1 (open question): specs/ownership-rules/spec.md: the Weakens definition requirement lists field cases but does not define the weakening of a rule's 'when' expression, the 'unless' body or the 'users' list when a write adds a user; ADR 225 decision 9 defines the fields and the spec leaves the mapping of each field to weaken or tighten open, so the shared function cannot be built right without the answer. Resolution: answered: Weakens definition now maps when, unless and users: widened when or unless weakens, narrowed when or unless tightens, removed user or group weakens, added user tightens; covered in specs and design decision 2.
- R2.2 (open question): specs/ownership-rules/spec.md: the 'Weakening write with access token' requirement says a tighten with a token is accepted, but no scenario states what happens when an access token creates a rule with mode evaluate that a later enforce change would tighten, or when a token deletes a rule (delete is not classed as weakening or tightening in the Weakens definition), so the delete path has no defined auth rule for access tokens. Resolution: answered: Delete is classed as weakening and create as non-weakening; added delete-with-token and create-evaluate scenarios.
- R2.3 (adr conflict): proposal.md ADR check: ADR 212 decision 3 (owner) says a change that reduces security needs a fresh MFA step; the spec's Weakens definition covers only the approval burden (count, mode, paths, freshAuthSeconds) and omits the 'Weakens definition' of security reduction for the destroy path; ADR 225 decision 5 requires a fresh step for an approval of a change that holds a destroy, which the spec does not carry into ownership-rule writes. Resolution: answered: Added destroy fresh-step scenario and ADR 225 decisions 2 and 5 to adr_check; weakens keeps the approval-burden mapping and approvals of a destroy still need fresh auth.
- R2.4 (owner decision conflict): proposal.md and tasks.md: the change is stated to block the first provider release in issue #967, while ADR 270 decision 5 (owner) orders work as collection update route, ownership rule routes, create-only writes and access-token writes to gate configuration; the change lists collection update and create-only writes as out of scope, so the owner order is not met by this change and the scope cannot be judged against the owner decision. Resolution: answered: ADR 270 decision 5 added to adr_check; out_of_scope now names collection update and create-only writes as separate changes of the same owner order, not skipped work.
- R2.5 (failure mode): specs/ownership-rules/spec.md: the 'Delete racing put' scenario allows either 412 or 404 but does not state which side wins when the put creates a new rule on an ID the delete just removed; the port-adapter layer in design.md claims a version compare, yet the scenario does not require it. Resolution: answered: Race scenario now requires version compare with one winner and 412 or 404 for the loser with no partial write.
- R2.6 (missing scenario): specs/ownership-rules/spec.md: no scenario for a list or read of ownership rules by a caller who holds the read action on the namespace but not on the rule object, nor for a list with an invalid page token, so the authorization-object rule of ADR 210 decision 10 and the paging errors are unspecified. Resolution: answered: Added list and read authorization and invalid page token scenarios.
- R2.7 (wrong claim): proposal.md ADR check: ADR 227 decision 6 is quoted as 'Errors' with the 403 rule, but the proposal's 'Changed by' note and the ADR 212 quote are not traceable to the given decision text for decision 3 in the index; the claim that ADR 212 decision 3 'follows the fresh-authentication rule for access-token writes' is not supported by the quoted decision, which says nothing about access tokens. Resolution: answered: ADR 212 effect now says the token rule comes from ADR 227 decision 6; quotes are exact spans from the pack.
- R2.8 (open question): design.md Decisions 2: the design says a write that does not weaken security works with a session or an access token, but ADR 270 decision 7 says the CLI and providers sign in with an access token with no exemption, and does not say whether a step-up sign-in is required for tightening; the provisional answer is not recorded. Resolution: answered: Design decision 2 now records that a non-weakening write with an access token needs no step-up sign-in.
- R2.9 (scope): tasks.md 2.2 and 3.1: the tasks add the approval-rule and approval-default weakens check and their e2e step, while proposal.md puts approval rule and approval default route shapes out of scope; the tasks touch objects the proposal excludes. Resolution: answered: Tasks and out_of_scope now state that only the shared weakens evaluation touches approval rules and defaults, not their route shapes.
- R2.10 (missing scenario): specs/ownership-rules/spec.md: no scenario for an ownership rule put by a service principal or an agent, although ADR 354 says an agent never has a fresh authentication and ADR 225 decision 7 says an agent never approves; the write path for these principals is not defined. Resolution: answered: Added service-principal and agent write scenarios; an agent never has fresh auth so its weakening write fails.
- M.37 (dependency): Ownership rules refer to path sets. This change is the single owner of the ownership-rule routes, but it serves only the namespace scope. Resolution: answered: Added depends_on add-named-path-sets-paths for path-set references and extended what_changes and specs to namespace, collection and unit scopes; this change stays the single owner of the ownership-rule routes.

## Open questions

none

Questions considered and answered:

- Do ownership rules need REST routes? ADR 225 decision 9 says an ownership rule is stored in the server and configured through the API, and ADR 270 decision 3 says providers manage it as code; the routes are required.
- What concurrency and list shape do the routes use? ADR 210 decisions 3 and 10 plus ADR 270 decision 8: resource paths, ETag with If-Match, 412 on stale match, list paging.
- When does a gate-configuration write work with an access token? ADR 212 decision 3 needs a fresh authentication for a weaker change; ADR 227 decision 6 gives the 403 code and says an access token never gives it. A non-weakening write needs no fresh step so it works with an access token.
- How does this change meet the owner order of ADR 270 decision 5? ADR 270 decision 5 orders collection update route, ownership rule routes, create-only writes and access-token writes; this change ships the ownership rule routes plus the shared weakens rule for access-token writes, and separate changes ship the other steps.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
