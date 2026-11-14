---
version: 1
---

Issue: #1507.

## Problem

The TFE workspace list already pages and counts in SQL. The round 3 review left a missing defense-in-depth comment and a test that does not derive its address context from t.Context(). This change fixes only those two nits.

## Evidence

- Handler.readsEveryUnit exists on main Proof: internal/api/tfe/workspaces.go:407 (func Handler.readsEveryUnit)
- The scope.Namespaces loop exists in the TFE workspaces handler Proof: internal/api/tfe/workspaces.go:444
- TestGateExactScopeWithinAuthorize exists on main Proof: pkg/ipallow/gate_list_test.go:271
- The partial index workspaces_live_name exists in the SQLite baseline Proof: pkg/store/sqlite/migrations/0168_baseline.sql:1650

## Fix boundary

- Add a short defense-in-depth comment in Handler.readsEveryUnit in internal/api/tfe/workspaces.go naming the Authorize check and the scope.Namespaces loop as independent layers
- Pass t.Context() as the parent of the address context in TestGateExactScopeWithinAuthorize in pkg/ipallow/gate_list_test.go
- File follow-up issues for the allowlist-cost design and the frozen-baseline index comment; no edits to frozen baselines now

Out of scope:

- Per-namespace exactness from ipallow.Gate after the address check; tracked by a new follow-up issue with its own design
- Comment on workspaces_live_name at baseline collapse; tracked by a new follow-up issue, no edit to frozen baselines now
- Any change to TFE list paging, counting, or authorization

## Check

- In internal/api/tfe/workspaces.go add a short defense-in-depth comment in Handler.readsEveryUnit that names the Authorize unit:read check and the scope.Namespaces loop as independent layers Check: `rg -n "Defense in depth" internal/api/tfe/workspaces.go` shows the new comment. Confirmed by: rg output.
- In pkg/ipallow/gate_list_test.go derive the address context in TestGateExactScopeWithinAuthorize from t.Context(); add a parent parameter to the helper when it has none and update its other callers in pkg/ipallow Check: `go test ./pkg/ipallow -run TestGateExactScopeWithinAuthorize -count=1`. Confirmed by: go test output.
- For the allowlist-cost nit in pkg/ipallow/gate.go:56 file a follow-up issue for per-namespace exactness from ipallow.Gate after the address check, with owner PM named in the body Check: `gh issue view <new-issue-number> --json title,body`. Confirmed by: gh output.
- File a follow-up issue tied to the baseline collapse: comment at pkg/store/sqlite/migrations/0168_baseline.sql:1650 and pkg/store/postgres/migrations/0168_baseline.sql:1908 that workspaces_live_name repeats the leading columns of workspaces_namespace_name_key because the partial predicate serves offset reads from the index Check: `gh issue view <new-issue-number> --json title,body`. Confirmed by: gh output.
- Confirm existing TFE handler tests stay green with no behavior change Check: `go test ./internal/api/tfe -count=1`. Confirmed by: go test output.
- Confirm ipallow package tests stay green Check: `go test ./pkg/ipallow -count=1`. Confirmed by: go test output.
- Run make check with zero findings Check: `make check`. Confirmed by: CI (make check).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Handler.readsEveryUnit reports whether the unit:read scope holds every unit (`internal/api/tfe/workspaces.go:407 (func Handler.readsEveryUnit)`)
- Gate.ScopeExact reports exactness of the allowlist gate (`pkg/ipallow/gate.go:56 (func Gate.ScopeExact)`)
- Authorizer.ScopeExact reports exactness of the access authorizer (`pkg/access/authorizer.go:250 (func Authorizer.ScopeExact)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Remove the scope.Namespaces loop as redundant with Authorize? No. The issue keeps the loop over scope.Namespaces as a second layer for access.Authorizer and asks for a comment only.
- Let ipallow.Gate report exactness per namespace in this change? No. The issue says per-namespace exactness needs its own design. This change files a follow-up issue and does not touch gate behavior.
- Edit the baseline migration comment for workspaces_live_name now? No. Baseline files are frozen until the collapse. This change files a follow-up issue tied to the collapse and does not edit the file now.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
