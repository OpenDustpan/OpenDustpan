## 1. Failing tests first

- [ ] 1.1 Add failing tests for state.resources and state.removed on tool-neutral operations. Check: TestStateWriteFactsHaveResourcesAndRemoved fails on main
- [ ] 1.2 Add failing tests for client-encrypted bodies with state.available false and continued write. Check: TestStateWriteEncryptedBodyContinues fails on main
- [ ] 1.3 Add failing tests for allow record in step transaction with audit entry record ID. Check: TestStateWriteRecordInStepTransaction fails on main
- [ ] 1.4 Add failing tests for refusal and approval-wait records in their own transaction. Check: TestStateWriteRefusalOwnTransaction fails on main
- [ ] 1.5 Add failing tests for fixed controls in the record. Check: TestStateWriteFixedControlsInRecord fails on main
- [ ] 1.6 Add failing tests for approval defaults at state.write with author and repeated signer refusal. Check: TestStateWriteApprovalDefaults fails on main
- [ ] 1.7 Add failing tests for limited authorizer on state.write unit, namespace, collection and installation objects. Check: TestLimitedAuthorizerStateWriteRoutes fails on main

## 2. Implementation

- [ ] 2.1 Fill state facts, cap counts at 10000 entries, and apply approval defaults in the guard. Check: `go test ./pkg/gate/...` passes
- [ ] 2.2 Write the record in the step transaction, the refusal in its own transaction, and list fixed controls. Check: `go test ./pkg/gate/... ./internal/api/...` passes
- [ ] 2.3 Wire LimitedAuthorizer for state.write routes and extend CheckAccess objects. Check: `go test ./...` passes for touched packages
- [ ] 2.4 Run the e2e step against the real CLI and the real directory. Check: `make e2e` state.write slice passes against the real CLI including a locked tofu apply through the TFE-compatible unlocked-write path
- [ ] 2.5 Document state.write facts, records, defaults and limits and check the simulate replay. Check: `dustpan gates test` replays stored state.write records and `make docs-lint` passes

## 3. Definition of done

- [ ] 3.1 Run wiring, route actions, e2e step, docs page and usability check from the docs page alone. Check: `make check` with zero findings
