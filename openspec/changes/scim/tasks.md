## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests for create, group, membership, mapping and source scim in NEW file pkg/scim/create_test.go. Check: `go test ./pkg/scim/... -run TestScimCreateUserGroupMembership`
- [ ] 1.2 Write failing tests for update, active, duplicate and concurrent create in NEW file pkg/scim/update_test.go. Check: `go test ./pkg/scim/... -run TestScimUpdateActiveDuplicate`
- [ ] 1.3 Write failing tests for auth scope, invalid body, hostile filter and path values, and rate limit in NEW file pkg/scim/auth_test.go. Check: `go test ./pkg/scim/... -run TestScimAuthScopeAndInvalidBody`
- [ ] 1.4 Write failing tests for delete, revocation, break-glass 409 and atomic audit in NEW file pkg/scim/delete_test.go. Check: `go test ./pkg/scim/... -run TestScimDeleteDisablesKeeps`

## 2. Build

- [ ] 2.1 Get the owner decision on SCIM timing and record it in proposal.md, section "Open questions", question 1. Build tasks are BLOCKED by it. (open question 1). Build tasks are BLOCKED by it. Check: The decision is recorded.
- [ ] 2.2 Build the port in NEW file pkg/scim/port.go, the service in NEW file pkg/scim/service.go and the adapter in NEW file internal/scimsvc/adapter.go. Check: `go test ./pkg/scim/... ./internal/scimsvc/...`
- [ ] 2.3 Wire the NEW routes with their authorization actions. Declare them in NEW file internal/api/scim_routes.go and add the adapter in NEW file internal/api/scim_handlers.go. Check: `go test ./internal/api/... -run TestScimRoutes`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add the e2e step. BLOCKED by the NEW route /api/dustpan/v1/scim/v2/Users and the NEW SCIM credential kind, which this change builds and which do not exist on main. Then a SCIM client creates a user with that credential, pulumi up and tofu apply pass, a DELETE follows, and the next CLI command gives 401. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the SCIM docs page and run each command on it. Check: `make docs-lint` and the docs command test
- [ ] 4.3 Do the usability check from the docs page alone. Check: Each error names one command. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
