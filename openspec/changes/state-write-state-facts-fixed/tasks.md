## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for state.resources and state.removed. Check: TestStateWriteFactsHaveResourcesAndRemoved fails on main
- [ ] 1.2 Add failing tests for client-encrypted bodies. Check: TestStateWriteEncryptedBodyContinues fails on main
- [ ] 1.3 Add failing tests for the allow record in the step transaction. Check: TestStateWriteRecordInStepTransaction fails on main
- [ ] 1.4 Add failing tests for refusal records. Check: TestStateWriteRefusalOwnTransaction fails on main
- [ ] 1.5 Add failing tests for fixed controls in the record. Check: TestStateWriteFixedControlsInRecord fails on main
- [ ] 1.6 Add failing tests for approval defaults. Check: TestStateWriteApprovalDefaults fails on main
- [ ] 1.7 Add failing tests for the limited authorizer on every object kind. Check: TestLimitedAuthorizerEveryRoute fails on main

## 2. Implementation

- [ ] 2.1 Fill state facts and apply approval defaults in the guard. Check: `go test ./pkg/gate/...` passes
- [ ] 2.2 Write records and list fixed controls. Check: `go test ./pkg/gate/... ./internal/api/...` passes
- [ ] 2.3 Wire LimitedAuthorizer for every route. Check: `go test ./...` passes for touched packages
- [ ] 2.4 Run the e2e step against the real CLI, including a locked tofu apply. Check: `make e2e` state.write slice passes
- [ ] 2.5 Document state.write facts, records, defaults and limits. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check wiring, route actions, e2e step, docs page and usability from the docs page alone. Check: `make check` with zero findings
