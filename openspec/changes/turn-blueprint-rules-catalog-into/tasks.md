## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing test for refusal with a decision record Check: TestSelfServiceBlueprintGateDeniesRequest
- [ ] 1.2 Add a failing check that no import, path or docs text names the old package Check: rg for the old package path over code, CLAUDE.md and docs returns nothing

## 2. Change

- [ ] 2.1 Route blueprint evaluation through gates at change.propose Check: `go test ./pkg/gate/...` passes on SQLite and PostgreSQL
- [ ] 2.2 Delete the old package and its mentions, only what exists Check: rg for the old package path returns nothing
- [ ] 2.3 Update the docs page Check: `make docs-lint` passes
- [ ] 2.4 Add hostile tests and the mutation test Check: Mutation test that removes the fail-closed branch fails

## 3. Definition of done

- [ ] 3.1 Run the e2e refusal step with a personal token against the real binary once the self-service request route exists on main Check: e2e step shows the named input and the decision record ID
- [ ] 3.2 Check the wiring, the route actions and the audit entry Check: The route test lists its actions
- [ ] 3.3 Run the full gate Check: `make check` reports zero findings
