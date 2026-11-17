## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing test: read an own-namespace record with a personal access token on the NEW route. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestDecisionRecordReadReturnsStoredRecord fails on main because the NEW route is missing.
- [ ] 1.2 Failing test: in-scope read without the action gives the 404 of an unknown ID. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestDecisionRecordReadDeniedGives404
- [ ] 1.3 Failing test: foreign, malformed, expired and unauthenticated reads. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestDecisionRecordReadForeignGives404
- [ ] 1.4 Failing test: installation record read on the NEW installation route. Write it in the NEW file internal/api/decisionrecord/decisionrecord_test.go. Check: TestInstallationDecisionRecordRead
- [ ] 1.5 Failing test: refusal names the record and no change-order command. Write it in the NEW file internal/api/stategate/refusal_test.go. Check: TestApprovalRefusalNamesRecordRead

- [ ] 1.6 SECURITY. Failing hostile test: the service rejects a non-gdr- ID and a forged scope with a sentinel, and the scope recheck after the load rejects a record of another scope. Write it in the NEW file internal/api/decisionrecord/service_hostile_test.go. Check: TestDecisionRecordServiceRejectsHostileInput
- [ ] 1.7 SECURITY. Failing hostile test: the port query gets an ID with SQL metacharacters and returns no row. Write it in the NEW file internal/api/decisionrecord/port_hostile_test.go. Check: TestDecisionRecordPortQueryHostileID

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.0 SECURITY. Build the NEW read service in the NEW file internal/api/decisionrecord/service.go. It checks the gdr- ID and the scope, loads the record, and checks the scope again after the load. Build the NEW port query in the NEW file internal/api/decisionrecord/port.go. It selects by ID and namespace with placeholders only. Check: TestDecisionRecordServiceRejectsHostileInput and TestDecisionRecordPortQueryHostileID pass.
- [ ] 3.1 Wire both NEW routes with their actions. In `.golangci.yml`, add `internal/api/decisionrecord` to the path-except list of the forbidigo rule "through apidesc" (one path, no rule weakened), because TestEveryDescribedAdapterReadsThroughApidesc in pkg/dustpan/apidesc_gate_test.go fails without it. Build the NEW handler files internal/api/decisionrecord/decisionrecord.go, internal/api/decisionrecord/operations.go and internal/api/decisionrecord/routes.go, and wire them in pkg/dustpan/decisionrecords.go, pkg/dustpan/dustpan.go, pkg/dustpan/apidesc.go and pkg/dustpan/route_declarations_internal_test.go. Add the NEW action installation:read-gates in pkg/identity/identity.go and pkg/access/access.go, and raise the catalog count from 127 to 128 in pkg/identity/actions_test.go. Check: Route test shows the actions of both routes.
- [ ] 3.2 Update the API description in `internal/api/apidesc/openapi.json` with `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`. Never edit it by hand. Check: `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` leaves no diff. Confirmed by: the driver's gates.
- [ ] 3.3 Docs page for the read call in NEW file docs/guides/decision-records.md. Check: `make docs-lint` passes and the curl command runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/read_route_decision_records_change_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 3.4 E2e step and usability check. Check: BLOCKED by the e2e fixture: a gate that refuses a write with the real OpenTofu CLI and a personal access token of a principal with the gate read action; the step reads the NEW route. A person's step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.5 Full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
