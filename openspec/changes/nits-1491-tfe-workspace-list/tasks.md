## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Nits

- [ ] 1.1 In internal/api/tfe/workspaces.go add a short defense-in-depth comment in Handler.readsEveryUnit that names the Authorize unit:read check and the scope.Namespaces loop as independent layers Check: `rg -n "Defense in depth" internal/api/tfe/workspaces.go` shows the new comment. Confirmed by: rg output.
- [ ] 1.2 In pkg/ipallow/gate_list_test.go derive the address context in TestGateExactScopeWithinAuthorize from t.Context(); add a parent parameter to the helper when it has none and update its other callers in pkg/ipallow Check: `go test ./pkg/ipallow -run TestGateExactScopeWithinAuthorize -count=1`. Confirmed by: go test output.

## 2. Follow-ups filed

- [ ] 2.1 For the allowlist-cost nit in pkg/ipallow/gate.go:56 file a follow-up issue for per-namespace exactness from ipallow.Gate after the address check, with owner PM named in the body Check: `gh issue view <new-issue-number> --json title,body`. Confirmed by: gh output.
- [ ] 2.2 File a follow-up issue tied to the baseline collapse: comment at pkg/store/sqlite/migrations/0168_baseline.sql:1650 and pkg/store/postgres/migrations/0168_baseline.sql:1908 that workspaces_live_name repeats the leading columns of workspaces_namespace_name_key because the partial predicate serves offset reads from the index Check: `gh issue view <new-issue-number> --json title,body`. Confirmed by: gh output.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Confirm existing TFE handler tests stay green with no behavior change Check: `go test ./internal/api/tfe -count=1`. Confirmed by: go test output.
- [ ] 4.2 Confirm ipallow package tests stay green Check: `go test ./pkg/ipallow -count=1`. Confirmed by: go test output.
- [ ] 4.3 Run make check with zero findings Check: `make check`. Confirmed by: CI (make check).
