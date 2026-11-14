## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing route tests for freeze create, end, list, job start refusal during freeze, break-glass start with break_freeze_reason, second create refused, and end with none active. Check: TestFreezeCreateThenJobStartRefused fails on main.
- [ ] 1.2 Add failing service tests for the freeze guard on job start, cancel, and phase dispatch in the same transaction as dispatch. Check: TestFreezeGuardAtJobStart fails on main.

## 2. Build

- [ ] 2.1 Add the freeze record keyed by namespace ID with creator subject, reason check, store, and unique partial index. Check: `go test ./internal/freeze/...` passes.
- [ ] 2.2 Add freeze create, end, and list routes with authorization actions, reason checks, and audit entries. Check: `go test ./internal/api/freezeapi/...` passes and the route test shows the declared action of each route.
- [ ] 2.3 Wire the freeze guard into job start, cancel, and phase dispatch with the break-freeze permission path and break_freeze_reason field. Check: `go test ./internal/jobsvc/... -run BreakFreeze` passes.
- [ ] 2.4 Edit the baseline migration of each dialect in place for the freeze table and index, never add a numbered migration. Check: Baseline migration edit in place per dialect keeps `sqlc diff` clean.
- [ ] 2.5 Update the API description route table with freeze manage, break-freeze, and read actions and the stored grant for break-freeze. Check: Route table test passes with freeze and break-freeze actions on the namespace object.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step against the real server over TLS on SQLite and PostgreSQL that uses the existing job start route and a personal access token. Check: e2e step with a personal access token calls POST /api/dustpan/v1/namespaces/{namespace}/jobs, creates a freeze, reads 403 on start and cancel, ends the freeze, and starts again over TLS on SQLite and PostgreSQL.
- [ ] 4.2 Write the docs page for freeze create, list, end, and break-glass start. Check: Docs page commands run as written for freeze create, list, end, and break-glass start.
- [ ] 4.3 Run the usability check of the freeze path. Check: Namespace administrator completes the path from the docs page alone and each error names the one command that helps.
- [ ] 4.4 Run the full check suite. Check: `make check` reports zero findings.
