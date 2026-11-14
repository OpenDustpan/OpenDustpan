## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing route tests for GET and PUT with runner-only grants, read-only on PUT, manage-only on GET, and cross-namespace grants in `internal/api/sourcefilter/sourcefilter_test.go`. Check: `TestSourceFilterRequiresSourceFilterActions` in `internal/api/sourcefilter/sourcefilter_test.go` fails on main with 200 where 403 is wanted.
- [ ] 1.2 Add failing audit test for the new manage action with actor and reason in `internal/api/sourcefilter/sourcefilter_test.go`. Check: `TestSourceFilterAuditUsesManageSourceFilter` in `internal/api/sourcefilter/sourcefilter_test.go` fails on main.

## 2. Fix

- [ ] 2.1 Change the source-filter route actions and the audit entry in the NEW internal source-filter service from source-filter-no-fetch-reads, with the handler in `internal/api/sourcefilter/sourcefilter.go` only passing through. Check: GET and PUT declare the NEW actions `namespace:read-source-filter` and `namespace:manage-source-filter` in the route table, applied in the NEW internal source-filter service after source-filter-no-fetch-reads lands.
- [ ] 2.2 Register the two NEW actions and carry them on the matching built-in roles, covered by `pkg/access/access_test.go:493`. Check: Action catalog in `pkg/access/access_test.go` lists the two NEW actions and each built-in role that carries a runner action for the same scope carries the matching new action.
- [ ] 2.3 Update the source-filter reference page in `docs/reference/source-filter-api.md` in the same pull request. Check: `docs/reference/source-filter-api.md` names the two NEW actions, the access-review preview step, and the query-both-action-names rule for audit history.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Cover GET, PUT, runner-only refusal, and the audit entry with an e2e step against the real server. Confirmed by: CI (e2e source-filter coverage). Check: E2e step in `e2e/coverage_sourcefilter.go` calls GET and PUT on /api/dustpan/v1/namespaces/{namespace}/source-filter with an access token, checks runner-only refusal and the audit entry.
- [ ] 4.2 Run the definition of done checks. Confirmed by: CI (make check). Check: `make check` with zero findings.
