## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the conformance row that leaves out a namespace. Check: `go test ./pkg/identity/identitytest -count=1`
- [ ] 1.2 Add store tests of the NOT IN filter and the bound. Check: `go test ./pkg/store/... -count=1`

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the gate, run the e2e step with an access token against GET /api/dustpan/v1/namespaces/{namespace}/collections, update the docs page, run the usability check. Check: `make check` with zero findings
