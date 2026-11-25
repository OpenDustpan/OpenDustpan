## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing conformance cases of the guard in pkg/store/storetest/break_glass_other_writes.go (NEW), run by every adapter: a revoke of the grant of a break-glass account that leaves one working account is refused with ErrNoWorkingBreakGlass; the same revoke when two stay passes; a revoke that does not lower the count passes below the floor; a revoke of a grant that is not a break-glass grant never counts. Check: `go test ./internal/store/sqlite/ -run BreakGlassOtherWrites -count=1` (NEW test) fails on main.
- [ ] 1.2 Add the failing cases for RemovePasskey in pkg/mfa/mfatest/passkeys.go (extend): the removal of the last passkey of a break-glass account is refused when it leaves fewer than two working accounts, and the removal of a passkey of another person never counts. Check: `go test ./internal/store/sqlite/ -run Passkey -count=1` fails on main.
- [ ] 1.3 Add the failing cases for SetNamespaceList in pkg/store/storetest/ipallow.go (extend) with a counter that reports a lower count after the write: the write is refused and the list stays. Check: `go test ./internal/store/sqlite/ -run IPAllow -count=1` fails on main.
- [ ] 1.4 Write the failing service tests in NEW file internal/accesssvc/break_glass_floor_test.go and the failing HTTP tests in NEW file internal/api/server/break_glass_floor_test.go: each of the three writes answers 409 with principal.no-working-break-glass and writes an audit entry. Check: `go test ./internal/api/server/ -run '^TestBreakGlassFloorOtherWrites$' -count=1` (own top-level test, NEW) fails on main.

## 2. Build

- [ ] 2.1 Call the set of the change keep-two-working-break-glass in RevokeGrant in internal/store/sqlite/access.go and internal/store/postgres/access.go, only for the grants that make a break-glass account work. Check: `go test ./internal/store/... -run BreakGlassOtherWrites -count=1` passes on sqlite and postgres.
- [ ] 2.2 Call the set in the shared body in internal/store/passkeystore/passkeystore.go (func RemovePasskey), only for break-glass accounts. Check: `go test ./internal/store/... -run Passkey -count=1` passes.
- [ ] 2.3 Call the set in internal/store/sqlite/ipallow.go and internal/store/postgres/ipallow.go after the list is replaced. Check: `go test ./internal/store/... -run IPAllow -count=1` passes.
- [ ] 2.4 Map ErrNoWorkingBreakGlass to 409 with the code principal.no-working-break-glass in internal/api/server/access.go, internal/api/server/ip_allowlist.go (func classifyIPAllowlist) and internal/api/server/passkeys.go. Check: `go test ./internal/api/server/ -run '^TestBreakGlassFloorOtherWrites$' -count=1` passes.
- [ ] 2.5 Add the list of the guarded writes to docs/guides/break-glass.md. Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Add the 409 answer with principal.no-working-break-glass to the three routes in the route table, then regenerate internal/api/apidesc/openapi.json (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check the wiring, the audit entries and the usability walk-through. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only)
