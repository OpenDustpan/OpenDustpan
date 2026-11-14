## 1. Failing tests first

- [ ] 1.1 Add failing domain tests for expiry valid, past, zero, normalized milliseconds, and past-maximum. Check: `go test ./internal/ephemeral/ -run TestNewLifetime` fails on main.
- [ ] 1.2 Add failing domain tests for the 24 hour warning, once with idempotency key retry, and short TTL below the window. Check: `go test ./internal/ephemeral/ -run TestWarning` fails on main.
- [ ] 1.3 Add failing e2e step in e2e/ephemeral_units_test.go for create, warning payload, destroy, 409 wait, review request approve success plus non-approver and self-approval refusals, cancel, extend, remove, and list expiry. Check: `go test ./e2e -run TestEphemeralUnits` fails on main with missing route.
- [ ] 1.4 Add failing hostile tests for review request release without grant at service and handler layers. Check: `go test ./internal/ephemeral/ -run TestApprove/HostileNoGrant` fails on main.

## 2. Build

- [ ] 2.1 Wire expiry store, warning sender with idempotency key, and atomic destroy job starter. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units` lists expiry in Unix milliseconds.
- [ ] 2.2 Wire extend and remove-expiry routes with declared authorization actions. Check: `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend` returns the new expiry or 422 past maximum.
- [ ] 2.3 Wire destroy wait on the RFC 114 review request with declared authorization actions and audit entries. Check: Waiting destroy holds with 409 and releases on review request approval, and refuses self approval.

## 3. Definition of done

- [ ] 3.1 Confirm wiring and routes with authorization actions for create, extend, and review request hold. Check: Expiry, warning, and destroy wiring reachable from the server with routes and declared authorization actions.
- [ ] 3.2 Run the e2e step against the real CLIs with runner pool and webhook sink. Check: `make e2e` passes the ephemeral step on SQLite and PostgreSQL through the real binary over TLS for Pulumi CLI and OpenTofu.
- [ ] 3.3 Publish the docs page for ephemeral stacks and workspaces with create, warning, review request approve, and extend commands. Check: Docs page commands run as written against the real binary over TLS and `make docs-lint` passes.
- [ ] 3.4 Run the usability check of the user path. Check: A Developer completes create, warning, approval, and destroy from the docs page alone with each error naming one command.
- [ ] 3.5 Run the full check gate. Check: `make check` reports zero findings.
