---
version: 1
---

Issue: #2885.

## Problem

A namespace administrator asks for a token on a principal and the reach check refuses it. The refusal reports how many objects the caller cannot read, which tells the caller about objects the caller cannot read. The administrator needs a refusal that names the denied decision and the one help operation, with no such count.

## Evidence

- The reach refusal helper exists on main. Proof: pkg/access/service.go:1572 (func principalReachRefusal)
- The self-service publish path exists on main. Proof: internal/selfservice/actions.go:279 (func PublishRequirements)
- The source-principal validation exists on main. Proof: internal/selfservice/blueprint.go:441 (func Blueprint.validateSourcePrincipal)
- The token routes exist on main. Proof: ANY /api/server/principals/{}/tokens
- The access grant test file exists on main. Proof: pkg/access/access_test.go:792

## Fix boundary

- Omit the unreadable-object count from the caller-visible reach refusal.
- Keep the count in the audit record for operators.
- Leave the action of the self-service source-principal read unchanged.

Out of scope:

- Change of the action of the source-principal read
- Other manage-tokens paths
- The decision logic of the reach check

## Check

- Write the failing test that a reach refusal has no count of unreadable objects in pkg/access/access_test.go:792. Check: NEW test `TestReachRefusalOmitsCount` fails on main and passes after the fix
- Change principalReachRefusal to omit the count while the audit record keeps it. Check: NEW test `TestReachRefusalOmitsCount` passes in `go test -race ./pkg/access/...`
- Wire the refusal text, update the docs page, run the e2e step, and run the usability check. Confirmed by: CI (e2e step and docs page) Check: Call ANY /api/server/principals/{}/tokens passing the reach check gate with an access token, see the documented refusal without a count, read the docs page, and complete the usability check from the docs page alone. Confirmed by: CI (e2e step and docs page)
- Run the full check. Confirmed by: CI (make check) Check: `make check` reports zero findings

Depends on:

- `access-service-grant-token-services` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The access service builds the principal reach refusal. (`pkg/access/service.go:1572 (func principalReachRefusal)`)
- The self-service publish path validates the source principal. (`internal/selfservice/blueprint.go:441 (func Blueprint.validateSourcePrincipal)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Is principal:manage-tokens the right action for the self-service source-principal read? The pack names the publish path and its source-principal validator but states no authorization rule that shows manage-tokens is wrong for this read. The change keeps the current action and alters only the refusal text.
- Is the unreadable-object count in the reach refusal acceptable? The pack states no rule that needs the count in the caller-visible refusal. The change omits the count from the refusal text and keeps the count in the audit record for operators.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
