## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first; record each difference in the pull request body. Read each fact of "What main already does" at its file, line and function on the current main. Do not edit design.md or proposal.md during the implementation. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Conformance cases for namespace cover and join-leave in pkg/manuallock/manuallocktest/suite.go. Check: TestManuallockNamespaceCover fails on main
- [ ] 1.2 Dispatch tests for wait, running phase, timeout and release in the NEW file pkg/manuallock/dispatch_test.go. Check: TestDispatchWaitsWithReasonLocked fails on main
- [ ] 1.3 Job-token tests for OpenTofu lock and Pulumi update start with race cases in pkg/manuallock/manuallock_test.go. Check: TestToolLockWithPreLockJobToken (NEW, in pkg/manuallock/manuallock_test.go) fails on main
- [ ] 1.4 Bulk route tests with mixed, duplicate and over-limit cases in the NEW file pkg/manuallock/bulk_test.go. Check: TestBulkLockPerItem fails on main
- [ ] 1.5 Import-hold preview test in the NEW file pkg/manuallock/importhold_test.go. Check: TestPreviewUnderImportHold fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the routes with the unit:lock action and audit entries. Check: go test ./pkg/manuallock/... ./internal/... -run Lock -count=1
- [ ] 3.2 E2e step: BLOCKED by the real runner pool e2e steps that the dependency change delivers; until then the step drives the real OpenTofu CLI with a personal token on PUT /api/dustpan/v1/namespaces/{namespace}/lock. Check: make e2e step refuses a covered workspace and names holder and reason
- [ ] 3.3 Extend the guide with namespace locks and dispatch. Check: docs page commands run as written
- [ ] 3.4 Usability check from the docs page alone, then make check. Check: make check reports zero findings
