---
version: 1
---

Issue: #3147.

Short form: bug. Owner lane: M9 (the planner role).

## Problem

**Persona: developer with the planner role.** The developer runs
`tofu apply` or `tofu destroy` on a workspace in a collection where they
hold the planner role. The server must refuse, and the CLI must show that
refusal, so that the developer knows that the role, not a missing
organization, stops the apply. Today no test checks what the CLI prints.
The implementation notes of the planner role say that the CLI stopped with
"organization not found" before it reached the server's 403.

## Evidence

- `e2e/e2e_planner_refused_test.go:134` (func plannerTofuWriteRefused)
  checks only `res.ExitCode == 0` for `apply` and `destroy`. Its comment
  (:126-132) says the CLI stops with a not-found organization, because the
  entitlement-set read needs `namespace:read`.
- On main the entitlement-set route also answers a holder of a grant on a
  collection or a unit in the namespace: `internal/api/tfe/routes.go:133-143`
  (Scope `ScopeDiscovery`), `internal/api/tfe/tfe.go:597` (func
  enforceDiscovery), and `holdsGrantInNamespace` (:705), which lists the
  objects of `unit:read` and `unit:read-outputs`. The planner role reads
  like a viewer (`pkg/access/roles.go:22-25`). So the cause in the comment
  may no longer apply, but no test shows which message the CLI prints now.
- `docs/guides/planner-role.md:59` says "`tofu apply` or `tofu destroy` as
  planner is refused." and :55 says a refusal "answers 403 with the error
  code `access.denied`".

## Fix boundary

- The e2e test checks the CLI output of `tofu apply` and `tofu destroy` as
  planner: it holds the refusal of the server (`access.denied` or the 403
  text that OpenTofu prints for it), and it does not hold
  `organization "<namespace>"` with "not found".
- If the test fails on main, the implementer finds the request that
  answers 404 from the e2e server log and records it in the pull request
  body. When that request is one of the discovery routes that ADR 374
  decision 1 names, the fix serves it with `ScopeDiscovery`, as the
  entitlement-set route is. When it is any other route, the implementer
  stops and reports to the PM: who may read what is a decision of the
  owner, not of this change.
- The stale comment of `plannerTofuWriteRefused` gives the behavior of main.
- Out of scope: any change of which principals may read a namespace or a
  workspace. ADR 374 decision 1 decides it.

## Check

`TestPlannerWriteRefused` in `make e2e` passes with the new output checks
(Confirmed by: CI), and `make check` reports zero findings (Confirmed by: CI).

## ADR check

**ADR 374: Namespace discovery from inside, and the window of the old event
secret** (owner-tagged, decision 1: "(owner, 2026-11-14)"). Changed by: -.
Current rule: this ADR. Quote: "The organization and discovery routes of a
namespace also answer a principal who holds a grant on a collection or on a
unit in that namespace. These are the TFE routes `GET
/api/v2/organizations/{org}/entitlement-set`, the organization read and the
workspace reads by organization". Effect: a planner with a grant on a
collection gets the entitlement set, so the CLI reaches the write and its
403. The change adds no route to that list. Conflict: none.

**ADR 212: The planner role** (decision 1). Quote, as the change
planner-role quotes it: a planner "reads state with the state actions that
the role gives, as viewer does". Effect: the planner holds `unit:read` on
the collection, which the discovery check reads. Conflict: none.

## Open questions

none. Considered: (1) give `namespace:read` to the planner role on a
collection grant (refused: it changes what a principal may see, and ADR 374
decision 1 already gives the discovery answer without it); (2) answer 403
in place of 404 on the entitlement set for a caller with no grant (refused:
ADR 374 keeps the same 404 as for an unknown namespace, so the answer is no
existence oracle, and a planner has a grant).
