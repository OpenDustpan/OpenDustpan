---
version: 2
---

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
| Put with a stale If-Match fails. | 412 with code precondition-failed. The rule is unchanged. Send the list again, merge the change and send the put with the new ETag. | `GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules` |
| Weakening write with an access token fails. | 403 with code mfa.fresh-step-required. The rule is unchanged. Sign in again as a person and repeat the write within the fresh window. | `opendustpan status` |
| Read of an unknown rule ID fails. | 404 with code not-found. Nothing changes. List the scope and use an ID from the list. | `GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules` |

Evidence for the claims above:

- No REST route reads or writes ownership rules today. Proof: Issue #1238: The gate package stores ownership rules (ADR 225 decision 9). No REST route reads or writes them.
- Access-token writes to gate configuration answer 403 with a plain message and no fresh-step code. Proof: Issue #1240: Every write to approval rules and approval defaults with an access token answers `403` with a plain message. It has no `mfa.fresh-step-required` code.
- The error code mfa.fresh-step-required exists in the catalog. Proof: internal/errcode/catalog_mfa.go:15

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules for list with paging, action ownership:read on the namespace
- New route GET /api/dustpan/v1/namespaces/{namespace}/ownership-rules/{id} for read with ETag, action ownership:read on the namespace
- New routes PUT and DELETE /api/dustpan/v1/namespaces/{namespace}/ownership-rules/{id} with If-Match, If-None-Match star for create, 412 on stale match and action ownership:manage on the namespace
- New routes for list, read, put and delete at collection scope, GET and PUT and DELETE /api/dustpan/v1/namespaces/{namespace}/collections/{collection}/ownership-rules[/{id}], with actions on the collection
- New routes for list, read, put and delete at unit scope, GET and PUT and DELETE /api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}/ownership-rules[/{id}], with actions on the unit
- One shared weakens definition for approval rules, approval defaults and ownership rules with 403 mfa.fresh-step-required on weaken with an access token, wired into the existing approval routes
- User documentation page for the new routes in the same pull request
- Route tests, service tests and an e2e step that drives each route first

Out of scope:

- Approval rule and approval default route shapes already exist; this change only wires the shared weakens check into those existing routes
- Gate evaluation and decision records
- CLI and provider releases that use the new routes
- Collection update route, shipped as a separate change of the ADR 270 decision 5 order

Depends on:

- add-named-path-sets-paths
- `add-named-path-sets-paths` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The namespace administrator must find the correct scope path for a rule; the docs page shows one copyable example for each scope.
- A stale 412 surprises a pipeline; the error names the read command that gives the new ETag.
- A weaken refusal with an access token surprises automation; the error names the fresh sign-in path.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Issuer settings type exposes ETag computation (`pkg/profile/issuer_settings.go:86`)
- Stable error code mfa.fresh-step-required exists in catalog (`internal/errcode/catalog_mfa.go:15`)
- Access test enforces that routes without actions are classified (`pkg/access/access_test.go:484`)

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
- Effect: The routes use resource paths under the namespace, page lists with cursor, use ETag with If-Match and If-None-Match, follow the JSON style guide and accept Idempotency-Key on writes.
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

## Open questions

Not spec-ready: a code check fails: task "Add the e2e step that drives each ownership-rule route first over TLS " runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Do ownership rules need REST routes? ADR 225 decision 9 says an ownership rule is stored in the server and configured through the API, and ADR 270 decision 3 says providers manage it as code; the routes are required.
- What concurrency and list shape do the routes use? ADR 210 decisions 3 and 10 plus ADR 270 decision 8: resource paths, ETag with If-Match for updates, If-None-Match star for create, 412 on stale match, 428 on missing precondition for updates, list paging.
- When does a gate-configuration write work with an access token? ADR 212 decision 3 needs a fresh authentication for a weaker change; ADR 227 decision 6 gives the 403 code and says an access token never gives it. A non-weakening write needs no fresh step so it works with an access token.
- How does this change meet the owner order of ADR 270 decision 5? This change ships the ownership rule routes plus the shared weakens rule wired into the existing approval routes; separate changes ship the collection update route and create-only writes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
