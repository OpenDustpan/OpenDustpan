---
version: 2
---

Issue: #929.

## Why

**Persona: namespace administrator.** A namespace administrator tries to decide which stacks and workspaces can read the outputs of each new stack or workspace. The server already has the route `GET/PUT /api/dustpan/v1/namespaces/{}/output-sharing` and the namespace settings routes. The administrator needs the namespace default for new units and rules at namespace and collection level, so that no one edits each producer by hand.

The user path this change must make work:

1. **API.** Read the namespace settings, then set outputSharing.newUnitDefault to nobody or namespace with PATCH. Read the effective value.
2. **API.** Write, read and remove a rule at namespace or collection level through the existing output-sharing route, by stable ID.
3. **CLI.** Run a Pulumi stack reference read and an OpenTofu remote state read from a job and see pass or deny as the rules say.
4. **Docs page.** Read the output sharing guide and the audit entries of rule changes and denied reads.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A job of unit B reads outputs of unit A with no rule that names it, or the starter lacks unit:read-outputs. | 403 with the error text that names the missing rule or grant. | `GET /api/dustpan/v1/namespaces/{namespace}/output-sharing` |
| A rule names an unknown collection or an object of another namespace. | 400 that names the target and stores no rule. | `GET /api/dustpan/v1/namespaces/{namespace}/collections` |

Evidence for the claims above:

- Issue 929 asks for the namespace setting, rules at namespace and collection level, the cross-unit read check and audit entries. Sharing between namespaces is out. Proof: #929 Scope: In: the namespace setting; sharing rules at namespace and collection level; the read check for outputs across units; audit entries. Out: sharing between namespaces.
- The owner decided the nobody default and one-step rules. Proof: #929 Decided: 15.6 (owner, 2026-10-18): output sharing of a new unit is a namespace setting, default nobody. A namespace or collection rule shares outputs in one step.
- The server already has an output-sharing route, the output actions in the built-in roles and the namespace settings route. Proof: Pack main list: route GET/PUT /api/dustpan/v1/namespaces/{}/output-sharing; test TestBuiltInRolesHoldTheOutputSharingActions at pkg/access/access_test.go:1723; route ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings.
- No non-test code names StackReference in pkg, internal or cmd. Proof: rg -n -F --glob '!*_test.go' 'StackReference' pkg internal cmd gives 0 matches (pack code_facts).

## What Changes

- Namespace setting outputSharing.newUnitDefault (nobody or namespace, default nobody) with its effective value in the settings read.
- Sharing rules at namespace and collection level on the existing output-sharing route, stored by stable ID. The route and the actions unit:read-outputs and unit:manage-sharing exist on main; this change adds no new route and no new action.
- Read check of both conditions at each output read for the Pulumi and OpenTofu paths.
- Audit entries for rule changes, default changes and denied reads.
- Docs page for output sharing.

Out of scope:

- Sharing between namespaces
- Console pages for sharing
- Job-token read paths of the dependency item

Depends on:

- #897

## Usability

Where the path still costs the person time or surprises them:

- An administrator may expect rules to follow a collection name. The docs say rules follow the stable ID.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Built-in roles hold the output sharing actions. (`pkg/access/access_test.go:1723`)
- unit:read alone does not read outputs. (`pkg/access/access_test.go:1778`)
- Settings error codes exist. (`internal/errcode/catalog_settings.go:7`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 1 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "**A job reads the outputs of another unit on two paths, each with two checks.** The server checks both conditions of a path at each read."
- Effect: Both checks run at each read, so a later change applies at the next read.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A namespace setting decides the rule of a new unit, and the default is "nobody" (15.6, option c).** The namespace setting `outputSharing.newUnitDefault` has the values `nobody` and `namespace`."
- Effect: This change implements the setting, the rules and the read check that these decisions give.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Write failing e2e steps for the Pulumi stack reference and OpenTofu re" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Run the e2e steps against the real CLIs on SQLite and PostgreSQL." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: contract method readOutputs: no requirement or scenario names it.

Questions considered and answered:

- What is the default for a new unit? ADR 273 decision 4: nobody.
- Does a default change rewrite existing units? No. ADR 273 decision 4 says the setting decides the rule of a new unit.
- Which action covers rule writes? ADR 273 decision 1 and the existing route use the actions that main already holds (pkg/access/access_test.go:1723). The change adds none.
- Which principal does a trigger start use? ADR 267 decision 14: the last saver, deny if missing.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
