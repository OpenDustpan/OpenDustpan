## 1. Failing tests first

- [ ] 1.1 Add failing domain tests for the one weakens function across approval rules, approval defaults and ownership rules. Check: `go test ./pkg/gate/... -run TestOwnershipWeaken -count=1` passes and shows each direction.
- [ ] 1.2 Add failing route tests for list, read, put and delete with actions, ETag, 412 on stale match and 403 mfa.fresh-step-required on weaken with an access token. Check: `go test ./internal/api/... -run TestOwnershipRuleRoutes -count=1` fails on main and passes after the fix.

## 2. Build

- [ ] 2.1 Add the shared weakens function with a test for each direction; it evaluates approval rules and defaults but adds no route for them. Check: `go test ./pkg/gate/... -count=1` passes.
- [ ] 2.2 Add the ownership-rule routes with actions, objects, ETag handling and audit entries. Check: Route test passes; `make policy-check` reports no route without an action.
- [ ] 2.3 Publish the routes in the API description. Check: API description diff shows the new paths; `make clients-check` passes.
- [ ] 2.4 Add the user documentation page for ownership rule routes and access-token writes. Check: Docs page renders; `make docs-lint` passes.

## 3. Definition of done

- [ ] 3.1 Add the e2e step that drives each ownership-rule route first with the real CLI over TLS. Check: e2e step drives list, read, put, delete, stale 412, tighten with token and weaken 403 against the real server.
- [ ] 3.2 Run the full check with race, lint and conformance suites. Check: `make check` reports zero findings with no new exclusion.
