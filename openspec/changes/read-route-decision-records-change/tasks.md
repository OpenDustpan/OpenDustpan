## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing test: read an own-namespace record with a personal access token on the NEW route. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestDecisionRecordReadReturnsStoredRecord fails on main because the NEW route is missing.
- [ ] 1.2 Failing test: in-scope read without the action gives the 404 of an unknown ID. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestDecisionRecordReadDeniedGives404
- [ ] 1.3 Failing test: foreign, malformed, expired and unauthenticated reads. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestDecisionRecordReadForeignGives404
- [ ] 1.4 Failing test: installation record read on the NEW installation route. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestInstallationDecisionRecordRead
- [ ] 1.5 Failing test: refusal names the record and no change-order command. Write it in the NEW file internal/api/stategate/refusal_test.go. Check: TestApprovalRefusalNamesRecordRead

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire both NEW routes with their actions. Check: Route test shows the actions of both routes.
- [ ] 3.2 Update the API description. Check: `make generate` leaves no diff.
- [ ] 3.3 Docs page for the read call. Check: `make docs-lint` passes and the curl command runs as written.
- [ ] 3.4 E2e step and usability check. Check: BLOCKED by the e2e fixture: a gate that refuses a write with the real OpenTofu CLI and a personal access token of a principal with the gate read action; the step reads the NEW route.
- [ ] 3.5 Full check. Check: `make check` reports zero findings.
