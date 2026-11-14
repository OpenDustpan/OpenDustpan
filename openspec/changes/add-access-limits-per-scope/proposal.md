---
version: 2
---

Issue: #906.

## Why

**Persona: namespace administrator.** A namespace administrator grants short-term access and caps risky actions with forbid-only limits. Today a grant has no end time and no per-scope ceiling exists, so stale grants keep access and broad rights cannot be narrowed without removing grants.

The user path this change must make work:

1. **Docs page.** Open the docs page for access limits and grant expiry.
2. **API.** Set expiresAt on a grant and export the access review to see the expired mark.
3. **API.** Save a forbid limit at namespace, collection or unit scope and test a refused call.
4. **API.** Read the limits and the access review through the API that the console reads.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Call refused by an access limit | 403 with gate.ErrLimited that names the limit ID; read the limit and ask an administrator to change it. | `GET /api/dustpan/v1/namespaces/{namespace}/access-limits/{id}` |
| Grant past expiresAt gives no access | 403; the access review marks the grant expired. Ask an administrator for a new grant. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Save with effect permit refused | 400 with gate.ErrInvalid; change the policy to forbid. | `PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id}` |

Evidence for the claims above:

- The issue asks for expiresAt on grants and forbid-only Cedar limits per scope with audit entries. Proof: #906 body: In: an optional `expiresAt` on a grant, enforced at check time and swept; access-limit objects and routes; evaluation of forbid policies in the access check; audit entries.
- Access-limit routes exist for namespace, collection and unit scopes. Proof: Pack main routes: GET /api/dustpan/v1/namespaces/{}/access-limits and DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/collections/{}/access-limits/{} ; coverage_gaps lists setUnitAccessLimit.
- ADR 223 defines the forbid-only per-scope limit model. Proof: docs/adr/0223-access-limits-cedar-forbid-policies-for-each-scope.md decisions 1, 2, 3 and 9.

## What Changes

- Optional expiresAt on a grant, enforced at check time and swept; access review marks expired rows.
- Access-limit evaluation after grants with forbid-only Cedar at installation, namespace, collection and unit scope.
- Refusals write decision records and audit entries; docs page ships in the same pull request.

Out of scope:

- Other grant conditions as stored fields besides expiresAt.
- Change of gate or approval behavior beyond the limit guard on gate configuration.
- Console page rendering of limits next to grants lives in the console repository.

Depends on:

- #900
- add-gate-configuration-routes
- `add-gate-configuration-routes` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused caller sees the limit ID but not how to request an exception; the docs page lists the one next step.
- Expiry uses UTC timestamps; the review shows the raw value to avoid confusion.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Group grant check reads access limits (`pkg/access/group_limits_test.go:91`)
- Policy test covers Cedar evaluation (`internal/policy/policy_test.go:465`)
- Denied calls use the access.denied error code (`internal/errcode/catalog_test.go:98`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 223: Access limits: Cedar forbid policies for each scope** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 5 (current), 7 (current), 8 (current), 9 (current), 10 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "Forbid only (owner). An access limit is one Cedar `forbid` policy. A limit set holds `forbid` policies only. The server refuses at save a policy with the effect `permit`, with `gate.ErrInvalid`."
- Quote: "For each scope. An access limit has a scope: the installation, a namespace, a collection or a unit."
- Quote: "After the grants. The `identity.Authorizer` checks the grants first."
- Quote: "Fail closed. A limit that gives a Cedar error refuses the call with `gate.ErrRuleFailed`, never with an allow."
- Quote: "A refusal writes a decision record. An access limit that refuses a call writes a decision record (ADR 229)."
- Quote: "Changes. A change of a limit needs the action of its scope (ADR 230 decision 2), a fresh authentication (ADR 227), and writes an audit entry."
- Effect: Implements forbid-only limits per scope, check after grants, fail-closed errors, refusal records, change controls and the gate-configuration guard.
- Conflict: none.

**ADR 313: Grant expiry, declared edges, profile objects and operator imports** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "ADR 313 extends: grant conditions."
- Effect: ADR 313 is owner-tagged and decides grant expiry; the change adds expiresAt under it and does not change its words.
- Conflict: none.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "What a row holds. Each row names the principal or the group, the role, the object and the time of the grant."
- Effect: A grant row gains an optional expiresAt; the review shows expired rows.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: Ships the docs page in the same pull request.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: ADR 313: the quote "ADR 313 extends: grant conditions." is not in the ADR file (copy it character for character from the decision text of the pack); a code check fails: requirement "limit-list": a limit with no number; give the number, the unit and the scope.

Questions considered and answered:

- Which grant conditions ship as grant fields? Only expiry; other conditions come through Cedar forbid limits (ADR 223 decisions 1 and 2).
- How do limits avoid locking out gate configuration? ADR 223 decision 10: both guards refuse at save.
- Where does limit evaluation sit? ADR 223 decision 3: after the grants, in one Cedar request.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
