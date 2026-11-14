## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Record the failing CallerGrants benchmark on main, median of 6, in pkg/store/sqlite. No route: the test records benchmark allocations only. Check: go test -run '^$' -bench CallerGrants -benchmem -count=6 ./pkg/store/sqlite shows allocs/op above 3146 on main
- [ ] 1.2 Add the NEW test TestCallerGrantsOmitsMemberSource in pkg/store/sqlite. It asserts the caller-grants query selects no member_source, member_source_id, or member_suspended column. No route: the test reads query text only. Check: go test ./pkg/store/sqlite -run TestCallerGrantsOmitsMemberSource fails on main
- [ ] 1.3 Add the NEW test TestCallerGrantsRefusesSuspendedMember in pkg/store/sqlite and the same test in pkg/store/postgres. No route: the test calls the store only. Check: go test ./pkg/store/sqlite ./pkg/store/postgres -run TestCallerGrantsRefusesSuspendedMember passes on both dialects

## 2. Fix

- [ ] 2.1 Narrow the caller-grants queries in pkg/store/postgres/queries.sql and the sqlite query source in pkg/store/sqlite to the old column set. Keep the suspended-member exclusion filter. No route: the test calls the store only. Check: go test -run '^$' -bench CallerGrants -benchmem -count=6 ./pkg/store/sqlite meets the quality budgets
- [ ] 2.2 Keep the wide member source fields in the group membership queries at pkg/store/postgres/queries.sql:1609 and the sqlite query source. Add the NEW test TestMembershipViewKeepsMemberSource on both dialects. No route: the test calls the store only. Check: go test ./pkg/store/... -run TestMembershipViewKeepsMemberSource passes
- [ ] 2.3 Regenerate sqlc output with make generate for pkg/store/sqlite/internal/db/queries.sql.go and pkg/store/postgres/internal/db/queries.sql.go. No route: the agent regenerates store code only. Check: make generate gives no diff beyond the narrowed caller-grants query. Confirmed by: the driver's gates.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run affected store tests with the race detector. No route: the test calls the store only. Check: go test -race ./pkg/store/sqlite passes
- [ ] 4.2 Confirm the tree passes all checks. No route: the agent runs static checks only. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
