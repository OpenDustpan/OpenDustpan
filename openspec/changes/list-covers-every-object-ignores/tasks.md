## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the conformance row that leaves out a namespace, in pkg/identity/identitytest/identitytest.go:176 (func CheckListWithinAuthorize). Check: `go test ./pkg/identity/identitytest -count=1`
- [ ] 1.2 Add store tests of the NOT IN filter and of the paged read at 200 names and one past it, in the NEW file pkg/store/namespace_exclusion_test.go. Check: `go test ./pkg/store/... -count=1`

- [ ] 1.3 SECURITY. Build the scope constructor that stores the exclusion list: it drops the list when All is false and sorts and dedupes names. Add it as NEW func in pkg/identity/scope.go (NEW file). Check: `go test ./pkg/identity -count=1`
- [ ] 1.4 SECURITY. Build the NOT IN filter of the namespace query for sqlite in pkg/store/sqlite/ipallow.go and for postgres in the NEW file pkg/store/postgres/namespace_exclusion.go. Names go in as placeholders, in chunks. Check: `go test ./pkg/store/... -count=1`
- [ ] 1.5 SECURITY. Build the gate that fills the exclusions for each request. It reads each page of NamespacesWithList, checks the client address, and fails closed on a read error. Put it in the NEW file pkg/ipallow/exclusions.go. Check: `go test ./pkg/ipallow -count=1`
- [ ] 1.6 SECURITY. Hostile test: a gate with a failing NamespacesWithList returns an error and no scope. Test name TestGateFailsClosedOnListError in the NEW file pkg/ipallow/exclusions_test.go. Check: `go test ./pkg/ipallow -run TestGateFailsClosedOnListError -count=1`
- [ ] 1.7 SECURITY. Hostile test: a namespace name with SQL text in the exclusion list changes no other row, on sqlite and postgres. Test name TestNamespaceExclusionHostileName in the NEW file pkg/store/namespace_exclusion_hostile_test.go. Check: `go test ./pkg/store/... -run TestNamespaceExclusionHostileName -count=1`
- [ ] 1.8 SECURITY. Hostile test: the caller access handler called from a refused address names the refused namespace as excluded. Test name TestCallerAccessFromRefusedAddress in the NEW file internal/api/server/caller_access_exclusion_test.go. Check: `go test ./internal/api/server -run TestCallerAccessFromRefusedAddress -count=1`

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the gate, run the e2e step with an access token against GET /api/dustpan/v1/namespaces/{namespace}/collections, update the docs page, run the usability check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
