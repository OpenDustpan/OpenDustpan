---
version: 1
---

Issue: #555.

## Problem

A site administrator removes a token-manager grant and expects the reach to end, but the service token keeps acting as ci-deploy until expiry.

## Evidence

- A service token keeps acting as ci-deploy after the maker grant is removed, until expiry up to 365 days. Proof: Issue #555 body: Her grant is removed. The token keeps acting as ci-deploy until it expires, which can be 365 days later.
- Service token routes exist on main. Proof: Pack main lists ANY /api/server/service-tokens.
- Service token creation requires the manage-tokens grant. Proof: Pack main lists pkg/accesstoken/scope.go:241 (func Service.requireManageTokens).

## Fix boundary

- Revoke a service token when its maker loses the last act-as path to its principal, by stable ID.
- Deny each later use with the existing error code token.outside-reach and write an audit entry.

Out of scope:

- Personal token lifetime
- Workload trust-rule management
- Access-review redesign

## Check

- Add failing test TestMakerLossDeniesServiceToken: make a service token, remove the maker grant, show use is denied with token.outside-reach. Check: `go test ./pkg/accesstoken/ -run TestMakerLossDeniesServiceToken -count=1` fails on main.
- Wire the NEW maker-path check into pkg/accesstoken/scope.go with one grant read per use and no cache. Check: `go test ./pkg/accesstoken/ -run TestMakerLossDeniesServiceToken -count=1` passes and BenchmarkCallerGrants shows no allocation regression.
- Show the ended token through the existing route GET /api/server/service-tokens (access token), add the docs page, and run the usability check from the docs page alone. Confirmed by: the driver's gates. Check: `GET /api/server/service-tokens` shows the token as ended.
- Run make check with zero findings. Confirmed by: the driver's gates. Check: `make check` reports zero findings.

Depends on:

- perf-benchmarkcallergrants-allocates-4-9
- `perf-benchmarkcallergrants-allocates-4-9` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Service token creation resolves the target principal through a dedicated resolver. (`internal/api/server/tokens.go:443 (func resolveServiceTokenCreate)`)
- Service token creation requires the manage-tokens grant. (`pkg/accesstoken/scope.go:241 (func Service.requireManageTokens)`)
- The access review lists token managers of a principal. (`pkg/access/review_service.go:697 (func Service.TokenManagers)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

1. Revoke a service token when its maker loses the last act-as path, or keep today's behavior as a declared residual? Decides: owner.
   - Options: Revoke at the loss of the last act-as path; long-lived credentials use a workload trust rule; Keep today's behavior and show 'can act as until <date>' in the access review.
   - Recommended: Revoke at the loss of the last act-as path; long-lived credentials use a workload trust rule Reason: The access review must match real reach, and a workload trust rule is the escape hatch for credentials that outlive a person.

Not spec-ready: a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: a security change needs STRIDE threats for each trust boundary ("threats.items").

Questions considered and answered:

- Should a service token stop when its maker loses the act-as path? No ADR in the pack decides token lifetime. The issue marks this as an owner decision, so it is an open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
