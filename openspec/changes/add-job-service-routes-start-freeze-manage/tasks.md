## 1. Failing tests first

- [ ] 1.1 Add failing route tests for freeze create, end, list, job start refusal during freeze, break-glass start, second create refused, and end with none active. Check: TestFreezeCreateThenJobStartRefused fails on main.
- [ ] 1.2 Add failing service tests for the freeze guard on job start in the same transaction as dispatch. Check: TestFreezeGuardAtJobStart fails on main.

## 2. Build

- [ ] 2.1 Add the freeze record keyed by namespace ID, the store, and the service with the active freeze check. Check: `go test ./internal/freeze/... ./internal/jobsvc/...` passes.
- [ ] 2.2 Add freeze create, end, and list routes with authorization actions, reason checks, and audit entries. Check: `go test ./internal/api/freezeapi/...` passes and the route test shows the declared action of each route.
- [ ] 2.3 Wire the freeze guard into job start with the break-freeze permission path. Check: `go test ./internal/jobsvc/... -run BreakFreeze` passes.

## 3. Definition of done

- [ ] 3.1 Add an e2e step against the real server over TLS on SQLite and PostgreSQL. Check: e2e step starts a job, creates a freeze, sees 403 on start, ends the freeze, and starts again.
- [ ] 3.2 Write the docs page for freeze create, list, end, and break-glass start. Check: Docs page commands run as written for freeze create, list, end, and break-glass start.
- [ ] 3.3 Run the usability check of the freeze path. Check: Namespace administrator completes the path from the docs page alone and each error names the one command that helps.
- [ ] 3.4 Run the full check suite. Check: `make check` reports zero findings.
