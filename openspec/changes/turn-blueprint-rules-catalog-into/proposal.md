---
version: 2
---

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
| Request refused by a blueprint gate | The server answers 403 with the gate reasons and the decision record ID. Read the gate, fix the answers, then send the request again. | `GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |
| Input rejected by the blueprint schema | The server answers 422 and names the input that the blueprint does not allow. Fix the answers and send the request again as a new revision. | `GET /api/dustpan/v1/self-service/blueprints/{namespace}` |

Evidence for the claims above:

- The issue states no tag shipped the removed shapes Proof: Issue 1036 text: No tag shipped them, so no data conversion exists (ADR 73).
- The self-service code may not be on main yet Proof: Issue 1036 text: The Catalog code may not be on main yet. If it is not, the pull request removes only what exists and this issue stays open for the rest.
- The gate error codes exist in the checkout Proof: internal/errcode/catalog_gate.go:17 defines gate.denied
- The old rule-engine directory is absent from the checkout Proof: Context pack paths list: the old rule-engine directory exists false; pkg/gate exists true

## What Changes

- Self-service evaluates blueprint rules through gates at change.propose, with one decision record for each completed evaluation
- Delete the old rule-engine package, its four adapters, its conformance suite, its depguard entry, and its docs mentions, where they exist
- Update the layout list of CLAUDE.md and GLOSSARY.md where the old package name remains
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

- A denied request names the input and links the decision record.
- No page or layout text names the old package after the removal.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error code gate.denied exists (`internal/errcode/catalog_gate.go:17`)
- Error code gate.invalid exists (`internal/errcode/catalog_gate.go:22`)
- Error code gate.not-found exists (`internal/errcode/catalog_gate.go:31`)
- make check target exists (`Makefile:14`)
- Placement rule does not authorize (TestPlacementRuleDoesNotAuthorize) (`pkg/access/access_test.go:841`)
- Gate actions held by built-in roles (TestBuiltInRolesHoldTheGateActions) (`pkg/access/access_test.go:1639`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 1, 2, 3, 6, 8).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 6 (current), 8 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "A gate is a stored rule at a fixed decision point (ADR 224)."
- Quote: "Stored in the server, configured through the API (owner)."
- Quote: "The word "policy" alone names no object (`GLOSSARY.md`)."
- Effect: Blueprint rules are stored gates at fixed decision points with one decision record for each evaluation, configured through the API.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: main_facts[3]: "where" is not file:line (func Name); a code check fails: task "Run the e2e refusal step with a personal token against the real binary" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Does removal of the old package conflict with an ADR? ADR 222 decision 8 and the issue scope say the old package goes in slice 5, so plain deletion is the answer.
- Is data conversion needed for stored rule objects? ADR 73 decision 4 and the issue text say no tag shipped the removed shapes, so deletion writes no shim and no data conversion.
- What if the self-service code is not on main yet? The issue says the pull request removes only what exists and the issue stays open for the rest.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
