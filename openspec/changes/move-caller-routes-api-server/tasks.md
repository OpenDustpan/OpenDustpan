## 1. Failing tests first

- [ ] 1.1 Add failing route tests for GET /me/access, GET /me/grants, and token list, create, delete, and rotate. Check: `go test ./internal/api/server -run TestCallerRoutesMoved -count=1` fails on main with 404 on /me paths
- [ ] 1.2 Add failing API description tests with WithQuery pageSize and pageToken labels and Idempotency-Key on each mutating token route. Check: `go test ./pkg/dustpan -run TestApiDescCoversMeRoutes -count=1` fails on main for missing description
- [ ] 1.3 Add failing tests for replay without secret, key reuse with different body or route, concurrent use of one key, and replay with an ended token. Check: `go test ./internal/api/server -run TestCallerIdempotencyConflict -count=1` fails on main

## 2. Move

- [ ] 2.1 Move handlers, tests, and e2e/coverage.go rows to /api/dustpan/v1/me paths with atomic rotate. Check: `go test ./internal/api/server/... -count=1` passes
- [ ] 2.2 Remove the six caller entries from the not-moved list in pkg/dustpan/apidesc_classes_test.go. Check: `go test ./pkg/dustpan -run TestClassCheck -count=1` passes with six fewer not-moved entries and fails when an old caller path stays served
- [ ] 2.3 Update docs pages that name old caller paths and confirm console account views call only /me paths before the next server tag. Check: `rg -n /api/server/caller docs e2e internal pkg` returns no hits; PM accepts console path updates before the next server tag
- [ ] 2.4 Reject cursor and limit with 400 naming pageSize and pageToken and declare default and maximum pageSize in the description. Check: `go test ./internal/api/server -run TestCallerPagingNames -count=1` passes

## 3. Definition of done

- [ ] 3.1 Add e2e steps for caller access, grants, token create, rotate, and delete each with Idempotency-Key, plus old-path 404 through the new paths. Check: `make e2e E2E_FOCUS=caller-routes` passes against the real server over TLS
- [ ] 3.2 Run definition of done: wiring, route authorization checks, e2e step, docs page, usability check from docs page alone. Check: `make check` passes with zero findings
