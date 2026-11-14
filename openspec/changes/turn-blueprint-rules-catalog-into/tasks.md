## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing test for refusal with a decision record in the NEW file pkg/gate/selfservice_blueprint_test.go. Check: TestSelfServiceBlueprintGateDeniesRequest
- [ ] 1.2 Add a failing check that no import, path or docs text names internal/policy (ADR files in docs/adr/ stay out of the check), in the NEW file pkg/gate/oldpackage_absence_test.go. Check: `rg -n 'internal/policy' --glob '!docs/adr/**' --glob '!openspec/**'` returns nothing after task 2.2

## 2. Change

- [ ] 2.1 Route blueprint evaluation through gates at change.propose (`PointChangePropose`, pkg/gate/gate.go:173): the evaluator in the NEW file pkg/gate/blueprint_evaluator.go, called from internal/selfservice/blueprint.go (replace policy.Effect at :143 and the switch at :680) and internal/selfservice/request.go. Check: `go test ./pkg/gate/... ./internal/selfservice/...` passes on SQLite and PostgreSQL
- [ ] 2.2 Delete internal/policy (internal/policy/policy.go, internal/policy/contracts.go, internal/policy/endpoint.go, internal/policy/webhook.go and internal/policy/policytest), its entries in .golangci.yml:291 and :715, and its uses in internal/api/selfservice/blueprint_schema.go, internal/selfservice/selfservicetest/selfservicetest.go and internal/selfservice/selfservicetest/blueprints.go. ADR files stay as they are. Check: `go build ./...` passes and the check of task 1.2 returns nothing
- [ ] 2.3 Update the existing guide docs/guides/publish-a-blueprint.md with the blueprint gates. Check: `make docs-lint` passes
- [ ] 2.4 Add hostile tests and the mutation test in the NEW file pkg/gate/blueprint_evaluator_hostile_test.go. Check: Mutation test that removes the fail-closed branch fails

## 3. Definition of done

- [ ] 3.1 BLOCKED by the self-service request route, which is not on main yet: the e2e refusal step with a personal token against the real binary waits for it Check: e2e step shows the named input and the decision record ID once the route exists. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Check the wiring, the route actions and the audit entry Check: The route test lists its actions
- [ ] 3.3 Run the full gate Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
