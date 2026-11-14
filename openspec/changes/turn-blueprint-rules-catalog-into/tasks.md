## 1. Failing tests first

- [ ] 1.1 Add a failing test for gate refusal with a decision record Check: TestSelfServiceBlueprintGateDeniesRequest fails on main
- [ ] 1.2 Add a failing check that no import, path or docs text names the old package Check: Absence test fails on main when old mentions exist, then passes after deletion

## 2. Change

- [ ] 2.1 Route blueprint evaluation through the gate package at change.propose Check: `go test ./pkg/gate/...` passes on SQLite and PostgreSQL
- [ ] 2.2 Delete the old package, its adapters, suite, depguard entry and docs mentions; when code is absent, remove only what exists and leave the issue open Check: Absence search over code, CLAUDE.md, GLOSSARY.md and docs returns nothing
- [ ] 2.3 Update the user docs page for blueprint gates and refusals Check: `make docs-lint` passes

## 3. Definition of done

- [ ] 3.1 Run an e2e step against the real CLI and the real directory Check: e2e self-service request with a denied input shows the named input and the decision record ID
- [ ] 3.2 Check the wiring, the route with its authorization actions, and the audit entry Check: The route test lists its actions and the audit entry exists
- [ ] 3.3 Walk the user path from the docs page alone and run the full gate Check: `make check` reports zero findings
