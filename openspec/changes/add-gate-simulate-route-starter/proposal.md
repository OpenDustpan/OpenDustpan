---
version: 2
---

Issue: #905.

## Why

**Persona: namespace administrator.** A namespace administrator tests policy before it blocks work. Today a draft gate can only go live in enforce mode and surprise teams. This change lets the administrator replay drafts against past decisions and start from packs that stay safe in evaluate mode.

The user path this change must make work:

1. **Docs page.** Read the docs page for simulate and starter packs.
2. **API.** Post draft gates and draft approval rules with a record filter to the simulate route.
3. **Console.** List starter packs and install one pack, then review its gates in evaluate mode in the console.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 on simulate when the caller lacks gate read rights | Access refused. The principal cannot use the simulate route. List the gates of the namespace to check gate read rights. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| 422 on simulate when the filter is invalid or the matched count is above 1,000 | Filter invalid. Narrow the time range and send the simulate request again. The server never truncates a range above the bound. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| 504 on simulate when the replay passes its timeout | Replay timed out. Narrow the time range and send the simulate request again. Nothing was stored. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |

Evidence for the claims above:

- Issue 905 asks for a simulate route over stored decision records and starter packs installed in evaluate mode. Proof: #905 Scope: POST .../namespaces/{namespace}/gates/simulate over stored decision records; starter packs shipped as data, installed in evaluate mode.
- ADR 229 defines the simulate route and the starter packs. Proof: docs/adr/0229-decision-records-simulate-and-starter-packs.md decisions 6 and 7.
- Issue 905 needs a docs page. Proof: #905 Acceptance criteria: Docs page (ADR 214).

## What Changes

- POST /api/dustpan/v1/namespaces/{namespace}/gates/simulate over stored decision records with stored and new results per record
- Starter gate packs shipped as data with list and install routes in evaluate mode with designed-against wording
- Docs page for simulate and starter packs

Out of scope:

- Compliance claims for packs; packs never say compliant
- Enforce mode install of pack gates
- Changes to live gates, jobs, or change orders from simulate

Depends on:

- add-gate-configuration-routes
- read-route-decision-records-change
- `add-gate-configuration-routes` (order of the milestone review)
- `read-route-decision-records-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Policy authors must learn the record filter fields; examples on the docs page show a decision point with a time range.
- Skipped records leave no trace, so an author with narrow reach sees fewer rows with no signal about hidden changes.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Gate service test covers job token at state.write. (`pkg/gate/service_test.go:109 (func TestEvaluateJobTokenAtStateWrite)`)
- Built-in roles hold gate actions. (`pkg/access/access_test.go:1639 (func TestBuiltInRolesHoldTheGateActions)`)
- Gate error codes exist for denied and not-found. (`internal/errcode/catalog_gate.go:7 (func gate.approvers-count-above)`)
- Gate error code gate.denied exists. (`internal/errcode/catalog_gate.go:17 (func gate.denied)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 229: Decision records, the simulate route and starter packs** (owner-tagged: decision 6, 7).

- Decisions that apply: 6 (current), 7 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "`POST .../namespaces/{namespace}/gates/simulate` takes draft gates, draft approval rules and a filter of stored records"
- Quote: "It evaluates the drafts against the stored fact documents, and gives for each record the stored result and the new result."
- Quote: "A starter pack is a set of gates and approval rules that the server ships."
- Quote: "A pack installs in `evaluate` mode."
- Effect: The change follows decisions 6 and 7. ADR 286 carries no numbered decisions and only renames catalog and promotion, so it does not alter decisions 6 and 7.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships its docs page in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How does a policy author test a draft gate before enforce? ADR 229 decision 6 replaces draft-rule replay with the simulate route; drafts are evaluated against stored fact documents with no stored change, skipped records leave no trace. The route needs read rights because it reads stored records and stores nothing.
- What wording and mode do starter packs use? ADR 229 decision 7 says packs are worded designed against a named framework, never claim compliance, install in evaluate mode, and each gate has a test file run by gate.RunTestFile.
- Does this change need a docs page? ADR 214 decision 1 needs a docs page in the same pull request for routes a person can see.
- Is the 1,000 record bound counted before or after the readability filter? ADR 229 decision 6 says the filter covers at most 1,000 records. The count is the matched stored records before the readability check, so the 422 refusal leaks no readable count.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
