## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing domain tests in the NEW file internal/ephemeral/lifetime_test.go for NewLifetime (NEW) valid inside 30 days from create, past, zero, normalized milliseconds, and past the maximum. Check: `go test ./internal/ephemeral/ -run TestNewLifetime` fails on main.
- [ ] 1.2 Add failing domain tests in the NEW file internal/ephemeral/warning_test.go for MarkWarned (NEW in internal/ephemeral; main has internal/cost/budget.go:302 MarkWarned) and BuildWarning (NEW): 24 hour warning, payload next steps, once with idempotency key retry, short TTL, and reset on extend. Check: `go test ./internal/ephemeral/ -run 'TestMarkWarned|TestBuildWarning'` fails on main.
- [ ] 1.3 Add the new file e2e/ephemeral_units_test.go for create, warning payload, destroy, 409 on destroy action, approve success plus non-approver and self-approval refusals, cancel, extend, remove, list expiry, and Pulumi CLI plus OpenTofu not found after destroy. BLOCKED by the new extend route until it ships. Check: `go test -tags e2e ./e2e -run TestEphemeralUnits` fails on main with missing route. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.4 Add failing hostile tests in the NEW file internal/ephemeral/approve_test.go for review request release without grant at service and handler layers. Check: `go test ./internal/ephemeral/ -run TestApprove/HostileNoGrant` fails on main.

## 2. Build

- [ ] 2.1 Wire expiry store, warning sender with idempotency key, and atomic destroy job starter with row lock in the NEW file internal/ephemeral/service.go. Declare the units list route in internal/api/objectcontext/routes.go and add its adapter in internal/api/objectcontext/serve.go. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units` lists expiry in Unix milliseconds.
- [ ] 2.2 Wire extend and remove-expiry routes with declared authorization actions. Declare the routes in internal/api/objectcontext/routes.go and add the adapter handlers in the NEW file internal/api/objectcontext/extend.go. Check: `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend` returns the new expiry or 422 past maximum.
- [ ] 2.3 Wire destroy wait on the review request with declared authorization actions and audit entries in the NEW file internal/ephemeral/approve.go. Declare the route in internal/api/objectcontext/routes.go and add the adapter in internal/api/objectcontext/serve.go. Check: Waiting destroy holds with 409 and releases on approval, and refuses self approval with denial audit.
- [ ] 2.4 Edit the baseline migration of each dialect in place for expiry time, warned flag, extension count, and state, in pkg/store/sqlite/migrations/0168_baseline.sql and pkg/store/postgres/migrations/0168_baseline.sql, with the store adapters in the NEW files pkg/store/sqlite/ephemeral.go and pkg/store/postgres/ephemeral.go and the queries in pkg/store/sqlite/queries.sql and pkg/store/postgres/queries.sql. Add the setting `ephemeralTimeToLive` to `unitsettings.NamespaceDefaults` (pkg/unitsettings/unitsettings.go:128) with its validation (at most 30 days) and its effective value in the answer of GET /api/dustpan/v1/namespaces/{namespace}/settings/unit-defaults (internal/api/settings/routes.go:85). Check: `sqlc diff` is clean for both dialects.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Confirm wiring and routes with authorization actions for create, extend, and review request hold. Check: Expiry, warning, and destroy wiring reachable from the server with routes and declared authorization actions.
- [ ] 4.2 Run the e2e step with a service token against GET /api/dustpan/v1/namespaces/{namespace}/units and POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend with runner pool and webhook sink; BLOCKED by the new extend route until it ships. Check: `make e2e` passes the ephemeral step on SQLite and PostgreSQL through the real binary over TLS for Pulumi CLI and OpenTofu. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Publish the docs page for ephemeral stacks and workspaces with create, warning, approve, and extend commands. Check: Docs page commands run as written against the real binary over TLS and `make docs-lint` passes. Confirmed by: CI (an e2e step in the NEW file e2e/ephemeral_units_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.4 Run the usability check of the user path. Check: A Developer completes create, warning, approval, and destroy from the docs page alone with each error naming one command. A person's step.
- [ ] 4.5 Run the full check gate. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
