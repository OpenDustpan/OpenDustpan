## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing domain tests for the one weakens function across approval rules, approval defaults and ownership rules. Check: `go test ./pkg/gate/... -run TestOwnershipWeaken -count=1` fails on main and shows each direction after the fix.
- [ ] 1.2 Add failing route tests for list, read, put and delete with actions, ETag, 412 on stale match and 403 mfa.fresh-step-required on weaken with an access token. Check: `go test ./internal/api/... -run TestOwnershipRuleRoutes -count=1` fails on main and passes after the fix.

## 2. Build

- [ ] 2.1 Add the shared weakens function with a test for each direction and for undecidable expressions failing closed. Check: `go test ./pkg/gate/... -count=1` passes.
- [ ] 2.2 Add the ownership-rule routes with actions, objects, ETag handling and audit entries. Check: The route test passes and reports no route without an action.
- [ ] 2.3 Wire the shared weakens check into the existing approval-rule and approval-default routes with no change to their shapes. Check: `go test ./internal/api/... -run TestGateWeakenWiring -count=1` passes.
- [ ] 2.4 Publish the routes in the API description. Check: `make clients-check` passes.
- [ ] 2.5 Add the user documentation page for ownership rule routes and access-token writes. Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add the e2e step that drives each ownership-rule route first over TLS against the real server with an access token: list, read, put, delete, stale 412, tighten with token, weaken 403. Check: `make e2e` runs the step and passes.
- [ ] 4.2 Run the full check with race, lint and conformance suites, and confirm a person completes the user path from the docs page alone. Check: `make check` reports zero findings with no new exclusion.
