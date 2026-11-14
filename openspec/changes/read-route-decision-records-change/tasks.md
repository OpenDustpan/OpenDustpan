## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing test: read an own-namespace record. Check: TestDecisionRecordReadReturnsStoredRecord fails on main because the route is missing.
- [ ] 1.2 Failing test: in-scope read without the action. Check: TestDecisionRecordReadDeniedGives403
- [ ] 1.3 Failing test: foreign, malformed, expired and unauthenticated reads. Check: TestDecisionRecordReadForeignGives404
- [ ] 1.4 Failing test: installation record read. Check: TestInstallationDecisionRecordRead
- [ ] 1.5 Failing test: refusal names the record and no change-order command. Check: TestApprovalRefusalNamesRecordRead

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire both routes with their actions. Check: Route test shows the actions of both routes.
- [ ] 3.2 Update the API description. Check: `make generate` leaves no diff.
- [ ] 3.3 Docs page for the read call. Check: `make docs-lint` passes and the curl command runs as written.
- [ ] 3.4 E2e step, usability check and full check. Check: E2e step against the real OpenTofu CLI passes and `make check` reports zero findings.
