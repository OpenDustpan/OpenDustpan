Issue: #1036.

## Why

**Persona: namespace administrator.** A namespace administrator publishes blueprints so teams create stacks and workspaces inside guard rails. The administrator needs one gate model for blueprint rules, with a decision record for each refusal, and no second rule engine to learn.

The user path this change must make work:

1. **API.** Publish a blueprint version with a rule, then request a unit from it through the API
2. **API.** Read the refusal that names the input and the decision record
3. **Docs page.** Read the updated blueprint gates page and repeat the request with fixed inputs

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Request refused by a blueprint gate | The server answers 403 with the reasons of the gate and the ID of the decision record. Fix the answers and send the request again. | `GET /api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions/{version}` |
| Input rejected by the blueprint schema | The server answers 422 and names the input that the blueprint does not allow. Fix the answers and send the request again as a new revision. | `GET /api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions/{version}` |

Evidence for the claims above:

- The gate domain package exists in the checkout Proof: paths entry marks pkg/gate as exists true
- The issue states no tag shipped the removed shapes Proof: Issue 1036 text: No tag shipped them, so no data conversion exists (ADR 73).
- The self-service code may not be on main yet Proof: Issue 1036 text: The Catalog code may not be on main yet. If it is not, the pull request removes only what exists and this issue stays open for the rest.

## What Changes

- Self-service evaluates blueprint rules through gates at change.propose, with one decision record for each evaluation
- Delete the old rule-engine package, its four adapters, its conformance suite, its depguard entry and its docs mentions, where they exist
- Update the layout list of CLAUDE.md and GLOSSARY.md where either names the old package
- Update the user docs page for blueprint gates

Out of scope:

- External Cedar service, external checks and starter packs
- Curated upstreams, ephemeral units, promotion, upgrade pull requests, job proxy

Depends on:

- #996
- #1003
- ADR 222 slice 1 issue
- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A denied request names the input and links the decision record, so the requester does not repeat blind edits.
- No page or layout text names the old package after the removal.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 110: The Catalog: a gateway of declarative requests on the job model** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 210 changes; ADR 218 extends; ADR 222 supersedes decision 9; ADR 225 changes decision 9; ADR 286 changes; ADR 305 extends; ADR 308 extends. Chain to the end: ADR 210, ADR 218, ADR 222, ADR 225, ADR 286, ADR 305, ADR 308, ADR 247, ADR 248, ADR 335, ADR 351, ADR 354, ADR 299, ADR 360, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 210, 218, 222, 225, 286, 305, 308.
- Quote: "One request, one unit. A Catalog request asks for one unit from one blueprint, in the namespace of the request."
- Quote: "A request has revisions. Each submit of a document makes a new revision of the request, with a number that grows by 1."
- Effect: The old Catalog words still name the old rule engine. Deletion waits for that RFC. Blueprint requests keep one request with revisions.
- Conflict: rfc_needed.

**ADR 114: The policy port and its four default adapters** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: ADR 192 extends decision 2; ADR 222 changes decision 8; ADR 222 supersedes decisions 1, 2, 3, 5, 6, 7, 9, 10, 11 and 12; ADR 224 changes decision 8; ADR 224 supersedes decisions 1, 2, 3, 5, 6, 7, 9, 10, 11 and 12; ADR 228 supersedes decision 5; ADR 229 supersedes decision 11; ADR 286 changes. Chain to the end: ADR 192, ADR 222, ADR 224, ADR 228, ADR 229, ADR 286, ADR 225, ADR 227, ADR 299, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 248, ADR 343, ADR 319, ADR 365, ADR 332.
- Current rule: this ADR, with ADR 192, 222, 224, 228, 229, 286.
- Quote: "One `deny` gives `deny`, with the reasons of each `deny`."
- Effect: ADR 222 supersedes the old combine rule. Gates keep the same join through approval rules.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The pull request updates the blueprint gates page in the same pull request.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 1, 2, 3, 6, 8).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 6 (current), 8 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "A gate is a stored rule at a fixed decision point (ADR 224)."
- Quote: "`change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Quote: "Gates, access limits, approval rules, approval defaults and ownership rules are objects of the server."
- Quote: "The word "policy" alone names no object (`GLOSSARY.md`)."
- Effect: Blueprint rules become stored gates at fixed decision points with a decision record, configured through the API.
- Conflict: none.

**ADR 73: Until 1.0, a change can break everything except the tool-compatible APIs** (not owner-tagged).

- Decisions that apply: 4 (extended).
- Changed by: ADR 322 changes decision 6; ADR 322 extends decision 4. Chain to the end: ADR 322, ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 322.
- Quote: "Do not write a shim, an alias, a second read path or a data conversion for a shape that no tag shipped."
- Effect: No tag shipped the removed shapes, so the change deletes code with no shim and no data conversion.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: The change writes no migration and no compatibility code.
- Conflict: none.

**ADR 286: Self-service, failover and promotion: three renames** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No alias, no shim and no data conversion keeps an old word (ADR 73)."
- Effect: The change keeps no alias for the old word.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The change deletes pkg/policy and its conformance suite, but ADR 110 decision 1 (current) says the Catalog 'imports pkg/job, pkg/policy and pkg/quota', and decision 10 (current) requires 'pkg/policy/policytest'. The proposal ADR check says 'Conflict: none' for ADR 110. An RFC is needed first. Resolution: answered: adr_check for ADR 110 now uses conflict rfc_needed with exact quotes; deletion waits for that RFC.
- R1.2 (open question): No requirement says how a blueprint rule becomes a stored gate object: when it is created, who owns it, how it is versioned with the blueprint, and how ADR 222 decision 6 (gates stored in the server, configured through the API) is met. The mapping of rule to gate cannot be built without this. Resolution: answered: Design states gates are stored at publish of a blueprint version, owned by the namespace and linked to that version per ADR 222 decision 6; specs cover refusal and audit.
- R1.3 (missing scenario): The proposal error table promises a 422 for an input the blueprint schema rejects and a 403 that names the missing grant, but the delta spec has no requirement or scenario for either. The fixed-input retry in the user path is also untested. Resolution: answered: Specs add schema refusal, grant refusal and fixed-input retry requirements with scenarios.
- R1.4 (failure mode): The design says the Cedar engine denies closed on evaluation error, but no requirement or scenario covers engine outage, timeout or an empty rule set. A fail-open path would not be caught by the spec. Resolution: answered: Specs add engine fail-closed and empty gate set requirements with scenarios.
- R1.5 (missing scenario): ADR 222 decision 2 lists change.planned and change.apply as decision points, but the change only gates change.propose. A blueprint rule edited or retired between propose and apply, or a revision retried after a restart, has no requirement. Resolution: answered: Design and considered state change.propose is gated here and change.planned and change.apply reuse the same stored gates through the existing job gate flow.
- R1.6 (wrong claim): Task 1.2 says TestPolicyPackageAbsent 'passes' with rg finding no pkg/policy path. On main, pkg/policy still exists, so the test must fail first, as the CLAUDE failing-test-first rule requires. The task states the opposite. Resolution: answered: Failing-tests task now states the absence test fails on main when old mentions exist.
- R1.7 (failure mode): The audit requirement writes an entry only on denial. An allow decision and its decision record have no audit entry, though each automatic action must write a record of who or what did it and why. Concurrent evaluations of the same revision are also not covered for a single decision record. Resolution: answered: Specs add allow audit entry and single record for concurrent evaluations with the same idempotency key.
- R1.8 (scope): The proposal says Catalog code may not be on main, so part of the removal may be deferred. The tasks and the spec-ready gate still assume it is present. The change does not say which tasks stay open in that case. Resolution: answered: Tasks and what_changes state that when code is absent the pull request removes only what exists and the issue stays open.
- R2.1 (adr conflict): The change deletes pkg/policy and policytest, but ADR 110 decisions 1 and 10 (current) require them: "It imports `pkg/job`, `pkg/policy` and `pkg/quota`." The change itself says an RFC is needed first, so the change is blocked and not spec-ready. ADR 222 slice 5 and the issue say the package goes, but no ADR changes ADR 110 decisions 1 and 10 explicitly. Resolution: answered: adr_check keeps rfc_needed on ADR 110 decisions 1 and 10; effect now states the change is blocked until that RFC lands.
- R2.2 (open question): Open question 1 (how a blueprint rule becomes a stored gate, who owns it, how it is versioned) has no Provisional decision. It marks 'Decides: owner' and blocks the build. ADR 222 decision 6 says gates are stored objects configured through the API; the mapping design is missing from design.md. Resolution: answered: Removed the open question. The design now gives a provisional decision: gates are stored at publish of a blueprint version, owned by the namespace, linked to that version. ADR 222 decision 6 supports it.
- R2.3 (open question): Open question 2 (re-evaluate at change.planned and change.apply) is open with no Provisional decision. The proposal says the change is not spec-ready, so no agent can build it. The design and specs cover only change.propose. Resolution: answered: Removed the open question. The change evaluates at change.propose only and leaves change.planned and change.apply to the existing job gate flow; this is stated in the design and in considered.
- R2.4 (wrong claim): The error table lists `curl .../gates` and `opendustpan doctor` as the one command that helps. The 403 row says 'run the reach check' but gives a curl command. A curl list of gates does not show the missing grant, and doctor does not fix a schema input error. These commands do not fit the errors. Resolution: answered: Error table now pairs each error with a read command that shows the record or the blueprint version.
- R2.5 (scope): The proposal says the Catalog code may not be on main, yet the spec adds requirements (422, 403, audit, single record) for code that may not exist. No spec requirement covers the removal itself (pkg/policy absent, no docs mention). Dependencies #996, #1003 and the slice 1 issue are not all listed; the slice 1 issue is missing. Resolution: answered: Added policy-removal requirement for absence of old package; depends_on now lists #996, #1003, and the slice 1 issue.
- R2.6 (failure mode): The design names a gateEvaluator type but leaves out a mutation test or a guard that a gate result can never become allow after an error. Fail-closed on an empty gate set is also not defined: a request with no gates may be allowed or denied. Resolution: answered: Design states fail-closed on engine error and empty answer, defines empty gate set as allow with record, and adds mutation test for the branch.
- R2.7 (missing scenario): The spec has no scenario for a warn or approve outcome of a blueprint gate, for a rule that a unit-scope gate overrides, or for a blueprint of another namespace (ADR 218 decision 3: not found). It also has no scenario for the retry after a 403 or an audit write that fails in the same transaction. Resolution: answered: Added warn, cross-namespace 404, retry, and atomic audit requirements with scenarios.
- R2.8 (missing scenario): The Single record requirement has no idempotency key rule. ADR 110 decision 7 gives the key `<request ID>/<revision>`; the spec does not say how concurrent evaluations are made to keep one record. Resolution: answered: Single-record requirement now states the idempotency key <request ID>/<revision>.
- R2.9 (scope): Task 2.2 checks only `rg -F policytest` over pkg, internal, cmd and docs. The issue criterion is `rg "pkg/policy"` finds nothing in code, CLAUDE.md or the docs; CLAUDE.md and GLOSSARY.md are not in the check. Task 1.2 also names a test that does not check for docs text. Resolution: answered: Tasks now check absence over code, CLAUDE.md, GLOSSARY.md, and docs.
- M.4 (dependency): The change rewrites the self-service evaluation that the blueprints change ships. It lists only external issues as dependencies. Resolution: answered: Added blueprints to depends_on and renamed the delta spec capability to blueprints so it touches the same capability as the blueprints change.

## Open questions

Not spec-ready: the change conflicts with ADR 110: an RFC comes first.

Questions considered and answered:

- Does removal of the old package conflict with ADR 110? ADR 222 supersedes the old decisions, but the current words of ADR 110 still name the old engine, so deletion needs an RFC first (rfc_needed).
- Is data conversion needed for stored rule objects? ADR 73 decision 4 and the issue text say no tag shipped the removed shapes, so plain deletion with no conversion is the answer.
- What if the self-service code is not on main yet? The issue says the pull request removes only what exists and the issue stays open for the rest.
- When does a blueprint rule become a stored gate, who owns it and how does it version? ADR 222 decision 6 says gates are server objects configured through the API. The design stores each blueprint rule as one gate at publish of the blueprint version, owned by the namespace and linked to that version.
- Do blueprint gates re-evaluate at change.planned and change.apply? This change gates change.propose. The same stored gates apply at change.planned and change.apply through the existing job gate flow, which this change does not alter.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
