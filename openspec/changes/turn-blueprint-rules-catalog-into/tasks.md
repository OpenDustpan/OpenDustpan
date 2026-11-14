## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing test for refusal with a decision record in the NEW file pkg/gate/selfservice_blueprint_test.go. Check: TestSelfServiceBlueprintGateDeniesRequest
- [ ] 1.2 Add a failing check that no import, path or docs text names the old package in the NEW file pkg/gate/oldpackage_absence_test.go. Check: rg for the old package path over code, CLAUDE.md and docs returns nothing

## 2. Change

- [ ] 2.1 Route blueprint evaluation through gates at change.propose in the NEW file pkg/gate/blueprint_evaluator.go. Check: `go test ./pkg/gate/...` passes on SQLite and PostgreSQL
- [ ] 2.2 Delete the mentions of the old package, only what exists, in the existing files CLAUDE.md, GLOSSARY.md and docs/blueprint-gates.md (NEW in task 2.3). Check: rg for the old package name returns nothing
- [ ] 2.3 Update the docs page in the NEW file docs/blueprint-gates.md. Check: `make docs-lint` passes
- [ ] 2.4 Add hostile tests and the mutation test in the NEW file pkg/gate/blueprint_evaluator_hostile_test.go. Check: Mutation test that removes the fail-closed branch fails

## 3. Definition of done

- [ ] 3.1 BLOCKED by the self-service request route, which is not on main yet: the e2e refusal step with a personal token against the real binary waits for it Check: e2e step shows the named input and the decision record ID once the route exists
- [ ] 3.2 Check the wiring, the route actions and the audit entry Check: The route test lists its actions
- [ ] 3.3 Run the full gate Check: `make check` reports zero findings
