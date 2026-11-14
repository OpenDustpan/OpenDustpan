## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing domain tests for NewLifetime valid inside 30 days from create, past, zero, normalized milliseconds, and past the maximum. Check: `go test ./internal/ephemeral/ -run TestNewLifetime` fails on main.
- [ ] 1.2 Add failing domain tests for MarkWarned and BuildWarning: 24 hour warning, payload next steps, once with idempotency key retry, short TTL, and reset on extend. Check: `go test ./internal/ephemeral/ -run 'TestMarkWarned|TestBuildWarning'` fails on main.
- [ ] 1.3 Add the new file e2e/ephemeral_units_test.go for create, warning payload, destroy, 409 on destroy action, approve success plus non-approver and self-approval refusals, cancel, extend, remove, list expiry, and Pulumi CLI plus OpenTofu not found after destroy. BLOCKED by the new extend route until it ships. Check: `go test ./e2e -run TestEphemeralUnits` fails on main with missing route.
- [ ] 1.4 Add failing hostile tests for review request release without grant at service and handler layers. Check: `go test ./internal/ephemeral/ -run TestApprove/HostileNoGrant` fails on main.

## 2. Build

- [ ] 2.1 Wire expiry store, warning sender with idempotency key, and atomic destroy job starter with row lock. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units` lists expiry in Unix milliseconds.
- [ ] 2.2 Wire extend and remove-expiry routes with declared authorization actions. Check: `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend` returns the new expiry or 422 past maximum.
- [ ] 2.3 Wire destroy wait on the review request with declared authorization actions and audit entries. Check: Waiting destroy holds with 409 and releases on approval, and refuses self approval with denial audit.
- [ ] 2.4 Edit the baseline migration of each dialect in place for expiry time, warned flag, extension count, and state. Check: `sqlc diff` is clean for both dialects.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Confirm wiring and routes with authorization actions for create, extend, and review request hold. Check: Expiry, warning, and destroy wiring reachable from the server with routes and declared authorization actions.
- [ ] 4.2 Run the e2e step with a service token against GET /api/dustpan/v1/namespaces/{namespace}/units and POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend with runner pool and webhook sink; BLOCKED by the new extend route until it ships. Check: `make e2e` passes the ephemeral step on SQLite and PostgreSQL through the real binary over TLS for Pulumi CLI and OpenTofu.
- [ ] 4.3 Publish the docs page for ephemeral stacks and workspaces with create, warning, approve, and extend commands. Check: Docs page commands run as written against the real binary over TLS and `make docs-lint` passes.
- [ ] 4.4 Run the usability check of the user path. Check: A Developer completes create, warning, approval, and destroy from the docs page alone with each error naming one command.
- [ ] 4.5 Run the full check gate. Check: `make check` reports zero findings.
