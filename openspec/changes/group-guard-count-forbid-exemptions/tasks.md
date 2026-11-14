## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing service tests in NEW file pkg/access/group_guard_forbid_test.go for an add to an unless-named group and a remove from a when-named group by a caller without the limited actions. Check: `go test ./pkg/access -run TestGroupGuardForbidWidening -count=1` (NEW test) fails on main
- [ ] 1.2 Write a test in pkg/access/group_limits_test.go that a group named by no limit keeps the current guard. Check: `go test ./pkg/access -run TestGroupGuardUnnamedGroup -count=1` (NEW test)
- [ ] 1.3 Write failing tests in pkg/access/group_limits_test.go for conflicting limits, same group in both clauses, disabled limit, unparsable limit set, rename, nested parent, child, cycle, and hidden limit ID. Check: `go test ./pkg/access -run TestGroupGuardCases -count=1` (NEW test) fails on main
- [ ] 1.4 Write a failing race test in NEW file pkg/access/group_guard_race_test.go where a limit save runs during a membership write. Check: `go test -race ./pkg/access -run TestGroupGuardConcurrentLimitSave -count=1` (NEW test) fails on main

## 2. Build

- [ ] 2.1 Build wideningCheck (NEW) in NEW file pkg/access/widening_check.go and apply it in pkg/access/service.go:656 (CheckGroupGrants) with the fenced namespace read and the versioned installation read, in the transaction of the reach check. Check: `go test -race ./pkg/access/... -count=1`
- [ ] 2.2 Write hostile parser tests in NEW file pkg/access/group_guard_hostile_test.go for each group reference form, with a mutation check that removing the guard fails them. Check: `go test ./pkg/access -run TestGroupGuardHostileParser -count=1` (NEW test)
- [ ] 2.3 Wire the guard into the member routes with their authorization actions and audit entries. Edit the route declaration in internal/api/apidesc and the adapter in internal/api/server/principals.go. Check: `go test ./internal/api/... -run TestRouteActions -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the docs page, run the e2e step, and do the usability check from the docs page alone. The e2e step uses POST /api/server/groups/{group}/members with an access token of a person principal that has group rights only, over TLS, then a real Pulumi CLI read and a real OpenTofu read. Check: `make e2e` and `make check` with zero findings
