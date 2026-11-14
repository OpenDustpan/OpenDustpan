## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing black-box tests for the typed errors in pkg/profile (NEW file pkg/profile/limiterror_test.go), with sentinel match and wrap cases. Check: `go test ./pkg/profile -run TestLimitError -count=1`
- [ ] 1.2 Write failing handler tests of the profile and registry answers with errcodetest.CheckAnswer (NEW file internal/api/profiles/args_test.go). Check: `go test ./internal/api/... -run TestErrorArgs -count=1`

## 2. Work

- [ ] 2.1 Add the NEW typed errors in pkg/profile/document.go and pkg/profile/document_check.go. Check: `go test ./pkg/profile -run TestLimitError -count=1`
- [ ] 2.2 Return the typed errors in pkg/store/sqlite/profile.go and pkg/store/postgres/profile.go, with conformance tests on both dialects. Check: `go test ./pkg/store/... -run TestProfileLimit -count=1`
- [ ] 2.3 Add the NEW typed errors in internal/registry/service/publish.go and pkg/registry/registry.go. Check: `go test ./internal/registry/... -run TestPublishLimits -count=1`
- [ ] 2.4 Add the NEW typed errors for G4 in pkg/collection, pkg/manuallock and pkg/objectcontext. Check: `go test ./pkg/collection ./pkg/manuallock ./pkg/objectcontext -count=1`
- [ ] 2.5 Add the NEW typed errors for G5 and G2 in pkg/job, pkg/store/internal/jobstore/rules.go, pkg/workspace and both workspace store adapters. Check: `go test ./pkg/job ./pkg/workspace -count=1`
- [ ] 2.6 Declare the arguments and the NEW codes in internal/errcode/catalog files and the frozen list, and update the golden files in internal/errcode/testdata/catalog. Confirmed by: the driver's gates. Check: `go test ./internal/errcode -run TestCatalog -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Regenerate the API description internal/api/apidesc/openapi.json and the client types. Confirmed by: the driver's gates. Check: `make clients-check`
- [ ] 4.2 Add the NEW docs page docs/reference/error-codes.md with the NEW codes and arguments. Check: `make docs-lint`
- [ ] 4.3 Run the usability check from the docs page and run make check. Confirmed by: the driver's gates. Check: `make check` with zero findings
