## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Hostile tests first (SECURITY)

- [x] 1.1 SECURITY. No route: the test calls the parse function only. Add `TestParseKindTable` (NEW) in pkg/principal/principal_test.go: person, service, workload and agent pass; `robot`, `Agent`, ` agent` and `agent` with a NUL byte are `principal.ErrInvalid`. Check: `go test ./pkg/principal/ -run TestParseKindTable -count=1` fails on main, because `KindAgent` is NEW.
- [x] 1.2 SECURITY. Add `TestCreatePrincipalRefusesAgentAndUnknownKinds` (NEW) in internal/api/server/principals_test.go: `POST /api/server/principals` with a session credential and the kinds `agent`, `robot`, `Agent`, an empty kind and `agent` with a newline each answer 400 `principal.invalid`, and the store holds no new row. Check: `go test ./internal/api/server/ -run TestCreatePrincipalRefusesAgentAndUnknownKinds -count=1` passes on main for the unknown kinds and still passes after the change for `agent`.
- [x] 1.3 SECURITY. No route: the test calls the audit validation only. Add `TestValidatePrincipalKinds` (NEW) in pkg/audit/audit_test.go: an entry with the principal kind `agent` passes, and `robot` is `audit.ErrInvalidEntry`. Check: `go test ./pkg/audit/ -run TestValidatePrincipalKinds -count=1` fails on main for `agent`.

## 2. Build

- [x] 2.1 SECURITY. Add `KindAgent` to `principal.Kind` and `principal.ParseKind` (pkg/principal/principal.go:84-101) and to `identity.Kind` and `identity.Kinds` (pkg/identity/identity.go:86-95); accept `agent` in `audit.Entry.validatePrincipal` (pkg/audit/audit.go:327) with the NEW constant `audit.PrincipalKindAgent`. Check: the tests of 1.1 and 1.3 pass.
- [x] 2.2 SECURITY. Give each switch over the kind an explicit `agent` case, or keep a default that refuses, with `TestKindSwitchesKnowAgent` (NEW) in the test file of each package: internal/subscriptionsvc/subscriptionsvc.go:382, internal/api/registry/handlers.go:163, internal/api/inventory/views.go:231, pkg/notify/sinks/sinks.go:691, and pkg/accesstoken/accesstoken.go:82 (`KindFor` refuses `agent` with `ErrNoTokenForKind`). Check: `go test ./internal/subscriptionsvc/ ./internal/api/registry/ ./internal/api/inventory/ ./pkg/notify/sinks/ ./pkg/accesstoken/ -run TestKindSwitchesKnowAgent -count=1` passes.
- [x] 2.3 SECURITY. Make the create route of principals (internal/api/server/principals.go:671) refuse the kind `agent` with `principal.invalid` after `principal.ParseKind`, with a comment that an agent record comes from the model connect request. Check: the test of 1.2 passes.

## 3. Definition of done

- [x] 3.1 Add one line to docs/guides/principals-and-groups.md: the kind `agent` exists, a person cannot create it through the API, and the list filters by it. Check: `make docs-lint` passes on each edited guide.
- [ ] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
