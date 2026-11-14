## 1. Failing tests

- [ ] 1.1 Add failing conformance tests for SCIM create user, create group, and add membership. Check: `go test ./pkg/scim/... -run TestScimCreateUserGroupMembership` fails on main.
- [ ] 1.2 Add failing tests for SCIM PATCH and PUT, membership removal, duplicate 409, and concurrent create. Check: `go test ./pkg/scim/... -run TestScimUpdateAndDuplicate` fails on main.
- [ ] 1.3 Add failing tests for missing or wrong SCIM scope 401 and for invalid or oversize body. Check: `go test ./pkg/scim/... -run TestScimAuthScopeAndInvalidBody` fails on main.
- [ ] 1.4 Add failing tests for SCIM DELETE disable of sessions and tokens and 401 on next CLI call. Check: `go test ./pkg/scim/... -run TestScimDeleteDisablesKeeps` fails on main.
- [ ] 1.5 Add failing tests for opt-in revocation, break-glass 409, idempotent DELETE, atomic audit, and rate limit. Check: `go test ./pkg/scim/... -run TestScimDeleteRevokesWhenSet` fails on main.

## 2. Build

- [ ] 2.1 Build the provisioning port, service, and SCIM adapter with stable subjects and group IDs. Check: `go test ./pkg/scim/... ./internal/scimsvc/...` passes.
- [ ] 2.2 Wire SCIM routes with auth actions and the public list check. Check: `go test ./internal/api/... -run TestScimRoutes` passes and route test lists actions.
- [ ] 2.3 Build audit entries for each SCIM create, change, membership change, disable, and revocation. Check: `go test ./internal/api/... -run TestScimAuditExport` passes and lists each SCIM entry.
- [ ] 2.4 Add e2e step: SCIM client creates user and group, existing grant on collection, pulumi up and tofu apply pass, DELETE, next CLI gives 401, revocation on revokes. Check: e2e step passes against the real binary over TLS with the real Pulumi CLI and the real OpenTofu CLI, using the collection grant that groups-roles-grants and collections-placement provide as a precondition.
- [ ] 2.5 Write the SCIM docs page in the same pull request. Check: Docs page builds and each command runs as written, with each 401, 404, 409, and 429 answer shown.

## 3. Definition of done

- [ ] 3.1 Run full check with zero findings. Check: `make check` reports zero findings.
- [ ] 3.2 Do the usability check. Check: Site administrator completes the path from the docs page alone and each error names the one command that helps.
