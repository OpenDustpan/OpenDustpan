---
version: 2
---

Issue: #904.

## Why

**Persona: namespace administrator.** A namespace administrator groups file paths into named sets so gates and ownership rules route network, docs and other areas to the right reviewers. Today each gate repeats raw glob text and a CLI write with no path list fails in an unclear way.

The user path this change must make work:

1. **API.** Save a path set for the namespace with PUT and If-Match, and read it back.
2. **API.** Push a change that edits modules/net/a.tf and read the path facts.
3. **Docs page.** Follow the docs page to name sets and use touches and only in a gate.
4. **E2e.** Drive the new path set routes with an access token and check the facts.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save of a path set with a bad ID, zero patterns, more than 64 patterns, or a reused wider scope ID fails. | The server answers 400 and names the bad ID or pattern. | `GET /api/dustpan/v1/namespaces/{namespace}/path-sets` |
| Save of a path set with a stale If-Match version fails. | The server answers 412. Read the path set again and save with the new version. | `GET /api/dustpan/v1/namespaces/{namespace}/path-sets` |
| A CLI write with no path list evaluates with missing path facts and a built-in template refuses. | The server names the gate that refused and says the path facts are missing. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |

Evidence for the claims above:

- Issue 904 asks for named path sets with server-computed paths.touches and paths.only facts and typed missing facts. Proof: Issue #904 scope: the path set object, its routes and audit entries; fact computation from the changed paths of a change order; typed missing when no path list exists.
- Issue 904 acceptance needs touches true for modules/net/a.tf against modules/net/**, only true when every path matches, and fail closed on missing. Proof: Issue #904 acceptance criteria.
- ADR 226 holds the path set and fact rules. Proof: docs/adr/0226-facts-typed-versioned-fact-documents.md
- Gate routes exist on main for namespace, collection and unit scope; no path set routes appear in the pack. Proof: Pack main list: GET /api/dustpan/v1/namespaces/{}/gates and the collection and unit gate routes; no path-sets route is listed.

## What Changes

- New routes, added by this change: list, get-one, PUT with If-Match and DELETE for path sets, at /api/dustpan/v1/namespaces/{namespace}/path-sets and /api/dustpan/v1/namespaces/{namespace}/path-sets/{id}, with matching routes at installation, collection and unit scope. Each route declares its authorization action and writes audit entries.
- First PUT carries no If-Match and assigns version 1; an update with no If-Match gives 428; a stale version gives 412.
- Server computation of paths.touches and paths.only from the changed paths of a change order.
- Typed missing path facts with available false and fail-closed built-in templates.
- Add the new docs page for path sets.

Out of scope:

- Other fact groups beyond paths.touches and paths.only
- Ownership rule and gate authoring beyond reading path facts
- Pull request automation and comment rendering
- Decision record read route, owned by read-route-decision-records-change

Depends on:

- #899
- read-route-decision-records-change
- `read-route-decision-records-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Pattern dialect surprises remain until the docs page shows tested glob samples.
- Missing path facts confuse CLI users unless the refusal names the gate.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Gate denied error code exists. (`internal/errcode/catalog_gate.go:17`)
- Gate invalid error code exists. (`internal/errcode/catalog_gate.go:22`)
- A state write test covers gate checks for a job token. (`pkg/gate/statewrite_test.go:804`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 3, 13).

- Decisions that apply: 3 (extended), 13 (extended), 14 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A path set is a stored object of a scope (the installation, a namespace, a collection or a unit) with an ID and 1 to 64 glob patterns."
- Quote: "A missing fact is a typed value (owner)."
- Quote: "A fact document holds at most 256 KiB."
- Effect: The change stores path sets and computes touches and only facts, returns typed missing values with fail-closed templates, and treats truncated paths as missing.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships a docs page in the same pull request.
- Conflict: none.

## Open questions

1. Which glob dialect matches changed paths against path set patterns? Decides: PM.
   - Options: Doublestar glob dialect with ** across separators; Gitignore dialect with anchored and negated patterns.
   - Recommended: Doublestar glob dialect with ** across separators. Reason: The issue example modules/net/** needs ** to cross separators, and a plain dialect with no negation is easy to explain and test.
2. What size caps refuse an oversize path set save and bound the changed path list? Decides: PM.
   - Options: Pattern document at most 8 KiB and changed path list at most 4096 entries; Pattern document at most 4 KiB and changed path list at most 2048 entries.
   - Recommended: Pattern document at most 8 KiB and changed path list at most 4096 entries. Reason: 64 patterns of 128 bytes fit in 8 KiB. A refusal that names the cap beats silent truncation.

Not spec-ready: a code check fails: task "E2E step: with a personal access token of a namespace administrator, c" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- What is a path set and what happens with no path list or a large list? ADR 226 decision 13 defines the set, decision 3 gives typed missing facts that fail closed, decision 14 bounds the document at 256 KiB.
- Is a docs page needed? ADR 214 decision 1: yes, the routes and errors are user visible.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
