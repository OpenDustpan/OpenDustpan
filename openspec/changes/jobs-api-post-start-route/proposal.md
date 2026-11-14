---
version: 1
---

Issue: #2379.

## Problem

A developer starts plan and apply jobs through the jobs API but the API description lists only unit:run-plan, so the developer cannot see that an apply start needs unit:run-apply.

## Evidence

- The jobs POST start route row declares only unit:run-plan. Proof: Issue #2379: The jobs POST start route row declares only `unit:run-plan`.
- The service authorizes the executor start action, so an apply start checks unit:run-apply. Proof: Issue #2379: The service (pkg/job/service.go, preAuthorize) authorizes the executor's own start action on the unit, so an apply start also checks `unit:run-apply`.
- Main has the start-action mapping. Proof: pkg/job/actions.go:129 (func startActionFor) and pkg/job/actions.go:149 (func startAction)

## Fix boundary

- Declare unit:run-apply for an apply start on the jobs POST start operation.
- State the per-kind mapping in the operation text.
- Add a test that fails when the declared set and the checked set differ.

Out of scope:

- New authorization checks
- New executor kinds
- Console changes

## Check

- Add NEW consistency test pkg/job/startactions_declared_test.go that compares the declared union with startActionFor and startAction in pkg/job/actions.go for each ExecutorKind value. Check: NEW pkg/job/startactions_declared_test.go TestStartActionsMatchDeclaration fails on main: declared set misses unit:run-apply.
- After the route-guard dependency lands, update the jobs POST start route row in internal/api/route and its operation text to declare the union {unit:run-plan, unit:run-apply} with the per-kind wording. Check: Route test through the route.Enforce path shows the union and NEW TestStartActionsMatchDeclaration passes.
- Regenerate the checked-in API description document and run the route and API description tests. Check: Route policy check and pkg/dustpan/apidesc_reads_test.go:39 pass. Confirmed by: CI (route and apidesc tests).
- Update the jobs API docs page for the start operation. Check: Developer reads the jobs docs page and sees both start actions with the per-kind wording. Confirmed by: CI (docs page check).
- Run the full check. Check: `make check` with zero findings. Confirmed by: CI (make check).

Depends on:

- small-hardening-26-oct-architecture-route-guard-enforcement

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main maps a start to its authorization action. (`pkg/job/actions.go:129 (func startActionFor)`)
- Main has the single start action helper. (`pkg/job/actions.go:149 (func startAction)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the change add a new authorization check? No. Issue #2379 states the service check is real and audited, so the fix declares the checked set, it adds no check.
- How is declaration drift prevented? The declared union is compared with the NEW test against startActionFor and startAction from pkg/job/actions.go, over every ExecutorKind value, so drift fails.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
