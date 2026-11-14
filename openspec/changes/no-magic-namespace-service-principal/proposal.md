---
version: 1
---

Issue: #2179, #2189, #2235.

## Problem

A namespace administrator tries to stop run triggers from starting plans by revoking access, but the namespace service principal holds its permission as a code rule that no one can list, audit, or revoke.

## Evidence

- The namespace service principal is decided by a code rule, not a stored grant. Proof: pkg/identity/namespace_service.go:101:type NamespaceServiceRule struct {
- The authorizer has a dedicated branch for that subject. Proof: pkg/access/authorizer.go:154 (func authorizeNamespaceService)

## Fix boundary

- Delete NamespaceServiceRule in pkg/identity/namespace_service.go and the authorizeNamespaceService branch in pkg/access/authorizer.go.
- Start every trigger, schedule catch-up, manual fire, and follows start as the stored run-as service principal, authorized by a stored grant of unit:run-plan.
- Record the run-as principal as the actor in the job and the audit entry; refuse with 403 and an audit entry when the grant is missing, revoked, suspended, deleted, or the trigger has no run-as principal.
- Update the code-rules note and the glossary lines that describe the namespace service principal.

Out of scope:

- Stored run-as field on triggers, its service-principal-only and author act-as validation, and same-request principal creation, built by the actor item in depends_on
- Drift checks, automatic remediation, and other housekeeping principals
- Trust-rule KeyFetcher suite and silent route, ADR 286 renames, branding preset, repository URL parser, collection placement fields, job expiry

## Check

- Add failing trigger-start authorization tests in NEW pkg/trigger/triggerstart_auth_test.go. BLOCKED by the actor item in depends_on for the stored run-as field. Check: NEW pkg/trigger/triggerstart_auth_test.go fails on main: allow, no-grant 403 with audit, revoked grant, suspended principal.
- Delete NamespaceServiceRule in pkg/identity/namespace_service.go and the authorizeNamespaceService branch in pkg/access/authorizer.go; route every trigger start through stored grants. Check: rg -n NamespaceServiceRule pkg internal returns no match.
- Wire the trigger start path in pkg/dustpan/triggers.go with its unit:run-plan authorization action and stored-grant check. Check: Route test passes for trigger start with stored-grant authorization actions.
- Update the note in docs/reference/code-rules.md for the removed rule and the glossary lines for the namespace service principal. Confirmed by: CI (make docs-lint). Check: docs/reference/code-rules.md names the removed rule as removed and `make check` reports zero findings.
- Run `make check` with zero findings. Confirmed by: CI (make check). Check: `make check` reports zero findings.

Depends on:

- adr-369-trigger-run-as-field

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the NamespaceServiceRule authorizer type. (`pkg/identity/namespace_service.go:101 (func Principal.IsNamespaceService)`)
- Main branches the access check for the namespace service subject. (`pkg/access/authorizer.go:154 (func authorizeNamespaceService)`)
- Main builds the trigger dispatcher config in the dustpan package. (`pkg/dustpan/triggers.go:175 (func triggerDispatcherConfig)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 14 (changed).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14; ADR 369 changes decision 14; ADR 370 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 286, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 369, 370.
- Quote: "The principal of a start that no person makes (question 3)."
- Effect: Follows decision 14 as changed by ADR 369 and ADR 370: no start runs as the in-memory namespace service principal; each trigger start runs as its stored run-as service principal.
- Conflict: none.

**ADR 369: The actor of a job: trust rules, conditions on token claims, and external service principals** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: ADR 370 changes. Chain to the end: ADR 370.
- Current rule: this ADR, with ADR 370.
- Quote: "Each automation names its run-as principal (owner, 2026-11-14). A run trigger, a schedule, a drift check or its schedule, an automatic remediation and an agent routine each name their own run-as principal."
- Effect: Follows decision 7: each trigger names its own stored run-as service principal; the job acts and reads as that principal through stored grants only.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Store the namespace service principal per namespace, keep NamespaceServiceRule as a cap, or delete the code rule? No exception and no cap. ADR 369 decision 7 replaces the code rule with a per-trigger stored run-as service principal. This change deletes NamespaceServiceRule and authorizeNamespaceService at once; every start passes the stored-grant check and fails closed.
- Which item builds the stored run-as field and its save-time checks? The stored run-as field on triggers, its service-principal-only validation, the author act-as check, and same-request creation are built by the ADR 369 actor item named in depends_on. This change deletes the rule and authorizes each start as that stored principal.
- What happens to existing triggers with no run-as principal? Existing triggers with no stored run-as principal fail closed with a refusal audit entry and no job. The operator lists them from the audit log. No backfill migration writes grants, so the baseline rule is not touched.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
