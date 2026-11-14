## 1. Failing tests first

- [ ] 1.1 Add Sessions port conformance in profiletest: other credential, ended session, limit, sweep, fence. Check: `go test ./pkg/profile/profiletest -run TestSessions -count=1` fails on main, passes after.
- [ ] 1.2 Add table tests for merge, final, checks, Expr, and marks through expressions and non-literal indexes. Check: `go test ./pkg/profile/... -count=1` covers merge order, null removal, final across imports, failed check, Expr values, and mark propagation.

## 2. Build

- [ ] 2.1 Implement Service.Open resolve without provider blocks, $neverStore on any value, open mode data or ephemeral, ErrNeverStore for consumer pulumi and mode data, and provider-block refusal. Check: `go test ./pkg/profile/... ./internal/api/profile/... -count=1` passes on sqlite and postgres.
- [ ] 2.2 Add SQL adapters for Sessions in both dialects, seal with the root data key, keep sessions out of backups, and sweep on schedule. Check: `go test ./pkg/store/... -count=1` passes; `TestFrozenMigrations` passes.
- [ ] 2.3 Wire new POST open and new GET session routes with profile:open actions and Idempotency-Key replay holding only the session ID. Check: `go test ./internal/api/profile/... -run TestRoutes -count=1` passes with %2F cases and action checks.
- [ ] 2.4 Write one audit entry of kind access in the same transaction for each open and each refused open with no value. Check: `go test ./pkg/audit/... -count=1` shows one access entry with no value for each allowed and refused open.

## 3. Definition of done

- [ ] 3.1 Add e2e profiles step against the real server. Check: `make e2e PROFILE=1` opens a profile, reads its session, and replays the key to the same answer.
- [ ] 3.2 Document open and session reads with errors and the one help command. Check: `docs/reference/profiles-api.md` documents open and sessions; examples run as written.
- [ ] 3.3 Run full gate: wiring, routes, e2e, docs, usability, make check. Check: `make check` reports zero findings with output in the PR.
