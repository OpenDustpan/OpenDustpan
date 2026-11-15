## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing route tests in NEW file internal/api/freezeapi/routes_test.go for freeze create, end, list, job start refusal during freeze, break-glass start with break_freeze_reason, second create refused, and end with none active. Check: TestFreezeCreateThenJobStartRefused fails on main.
- [ ] 1.2 Add failing service tests in NEW file internal/jobsvc/freeze_test.go for the freeze guard on job start, cancel, and phase dispatch in the same transaction as dispatch. Check: TestFreezeGuardAtJobStart fails on main.

## 2. Build

- [ ] 2.1 Extend the existing file internal/freeze/freeze.go with the freeze record keyed by namespace ID with creator subject, reason check, store, and unique partial index. Check: `go test ./internal/freeze/...` passes.
- [ ] 2.2 Add freeze create, end, and list routes in NEW declaration file internal/api/freezeapi/routes.go and NEW adapter file internal/api/freezeapi/handler.go with authorization actions, reason checks, and audit entries. Check: `go test ./internal/api/freezeapi/...` passes and the route test shows the declared action of each route.
- [ ] 2.3 Wire the freeze guard in NEW file internal/jobsvc/freeze.go into job start, cancel, and phase dispatch with the break-freeze permission path and break_freeze_reason field. Check: `go test ./internal/jobsvc/... -run BreakFreeze` passes.
- [ ] 2.4 Edit the baseline migration of each dialect in place, pkg/store/sqlite/migrations/0168_baseline.sql and pkg/store/postgres/migrations/0168_baseline.sql (the freeze table and its index, in name order), never add a numbered migration. Check: Baseline migration edit in place per dialect keeps `sqlc diff` clean.
- [ ] 2.5 Update the route table in internal/api/jobs/routes.go with freeze manage, break-freeze, and read actions (`make generate` then writes internal/api/apidesc/openapi.json) and the stored grant for break-freeze. Check: Route table test passes with freeze and break-freeze actions on the namespace object.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step in the NEW file e2e/add_job_service_routes_start_freeze_manage_test.go against the real server over TLS on SQLite and PostgreSQL that uses the existing job start route and a personal access token. Check: e2e step with a personal access token calls POST /api/dustpan/v1/namespaces/{namespace}/jobs, creates a freeze, reads 403 on start and cancel, ends the freeze, and starts again over TLS on SQLite and PostgreSQL. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the docs page in the NEW file docs/guides/freeze.md for freeze create, list, end, and break-glass start. Check: Docs page commands run as written for freeze create, list, end, and break-glass start. Confirmed by: CI (an e2e step in the NEW file e2e/add_job_service_routes_start_freeze_manage_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.3 Run the usability check of the freeze path. Check: Namespace administrator completes the path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.4 Run the full check suite. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
