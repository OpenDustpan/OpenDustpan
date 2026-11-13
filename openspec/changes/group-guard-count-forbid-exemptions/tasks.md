## 1. Failing tests first

- [ ] 1.1 Add failing service tests for add to unless-named group and remove from when-named group by a caller without the rights. Check: `go test ./pkg/access -run TestGroupGuardForbidWidening -count=1` fails on main, passes after the fix.
- [ ] 1.2 Add test that a group named by no limit keeps the current guard behavior. Check: `go test ./pkg/access -run TestGroupGuardUnnamedGroup -count=1` passes before and after.
- [ ] 1.3 Add failing tests for conflicting limits, unparsable limit set, and rename and remove and make again by stable ID, including a nested parent case. Check: `go test ./pkg/access -run TestGroupGuardConflictingLimits|TestGroupGuardParseFailsClosed|TestGroupGuardStableID -count=1` fails on main, passes after the fix.
- [ ] 1.4 Add failing test for a bulk and sync write that widens a limit through the same guard. Check: `go test ./pkg/access -run TestGroupGuardBulkSync -count=1` fails on main, passes after the fix.
- [ ] 1.5 Add failing concurrency test where a limit save races a membership write and the guard sees the new limit. Check: `go test -race ./pkg/access -run TestGroupGuardConcurrentLimitSave -count=1` fails on main, passes after the fix.

## 2. Build

- [ ] 2.1 Apply the widening check on stable group ID in one fenced transaction with a constructor that parses saved forbid policies, together with the reach check of check-reach-when-grant-membership in that same transaction. Check: `go test -race ./pkg/access/... ./internal/accesssvc/... -count=1` passes.
- [ ] 2.2 Add hostile parser tests for every Group reference form, including nested expressions. Check: `go test ./pkg/access -run TestGroupGuardHostileParser -count=1` passes, with one hostile input for each Group reference form and a mutation check that removal of the guard fails the test.
- [ ] 2.3 Wire the guard into the membership routes with declared authorization actions and audit entries that name the limit ID. Check: `GET /api/dustpan/v1/access/reach?principal=<subject>` returns the same shape before and after the change.
- [ ] 2.4 Document the guard, the needed rights, the site admin refusal, and the break-glass exemption. Check: Docs page runs as written against a dev server over TLS.

## 3. Definition of done

- [ ] 3.1 Run wiring check, route authorization check, e2e step with real CLI read after refused widening change, docs page check, and usability check from the docs page alone. Check: `make check` with zero findings.
