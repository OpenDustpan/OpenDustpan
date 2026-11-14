## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Write failing tests for forbid-only-save, collection forbid wins, scope-addition, installation-scope, never-allow and both gate guards. Check: TestAccessLimitPermitRefused fails on main, passes after the fix.
- [ ] 1.2 Write failing tests for grant expiry, check clock, sweep, review and history. Check: TestGrantExpiryReviewMarksExpired fails on main, passes after the fix.
- [ ] 1.3 Write failing tests for limit list, get, delete, decision records, label limits and break-glass. Check: TestLimitListGetDelete and TestBreakGlassExempt fail on main, pass after the fix.

## 2. implementation

- [ ] 2.1 Add expiresAt to the grant store, check and leased sweep. Check: `go test ./pkg/access/... ./pkg/gate/...` passes.
- [ ] 2.2 Add limit evaluation after grants, audit entries and the docs page. Check: `go test ./internal/...` passes; routes declare scope actions and fresh step.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Run the e2e step with the real server over TLS: a session credential calls PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id}, then GET /api/dustpan/v1/namespaces/{namespace}/units is refused with gate.ErrLimited. Add docs page and usability check. Check: `make e2e` passes the access-limit step.
- [ ] 4.2 Run the full check. Check: `make check` with zero findings.
