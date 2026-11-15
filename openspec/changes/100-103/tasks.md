## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server tests in NEW internal/servertest/jobs_test.go for create, single ClaimJob (NEW), cancel, and forward-only advance scenarios. Check: TestJobScenarios_CreateAdvance and TestJobScenarios_Cancel fail on main, pass after, with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` for the read baseline.
- [ ] 1.2 Add SQL adapters for job records by stable job ID with fence-checked writes. Check: TestJobStoreConformance fails on main, passes after, with fence-checked writes on pkg/job records.
- [ ] 1.3 Edit the baseline schema of each dialect in place for job tables and the active-job unique index, in the NEW job table files of each dialect under NEW internal/store/sql/ (NEW). Check: Baseline schema edit in each dialect passes sqlc diff for job tables and the active-job unique index.

## 2. Wiring and docs

- [ ] 2.5 SECURITY. Build the CreateJob allowlist and limits in NEW internal/jobsvc/create.go (NEW): allow only the executors write-back and runners, allow only phase names from pkg/job, allow at most 32 phases and at most 64 inputs, and reject all other values with the NEW error code job.invalid. Check: TestJobCreateAllowlist in NEW internal/jobsvc/create_test.go rejects an unknown executor, an unknown phase, 33 phases and 65 inputs.

- [ ] 2.1 Wire NEW tool-neutral jobs routes GET and POST /api/dustpan/v1/namespaces/{namespace}/jobs and the NEW cancel route in internal/api/jobs/routes.go (main declares the jobs routes there, at jobsPath; add only the routes and the cancel route that main does not have) with the NEW action unit:run-job and audit entries, plus openapi regen. Check: NEW POST /api/dustpan/v1/namespaces/{namespace}/jobs creates a job with access token holding the NEW action unit:run-job; BLOCKED by NEW jobs routes for the create call itself, read baseline on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` with access token.
- [ ] 2.2 Wire write-back advance in NEW internal/jobsvc/advance.go (NEW) under the server executor identity with idempotent phase reports. Check: Write-back advance verified with access token on the NEW jobs routes; BLOCKED by NEW jobs routes, read baseline on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` with access token.
- [ ] 2.3 Write docs page NEW docs/jobs.md for jobs: create, list, cancel, phases, and errors. Check: Docs page commands run as written in a fresh install, verified with access token reads on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job create examples. Confirmed by: CI (an e2e step in the NEW file e2e/100_103_docs_test.go runs each command of the docs page against the e2e test server). A person's step.
- [ ] 2.4 Measure job create latency target and record the result. Check: 500 ms p95 create to queued measured in internal/servertest with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` as the read baseline; BLOCKED by NEW POST /api/dustpan/v1/namespaces/{namespace}/jobs for the create call.

## 2a. Hostile tests

- [ ] 2a.1 SECURITY. Add the hostile test TestJobCreateRejectsExecutorShell in internal/servertest/jobs_test.go (NEW file, see task 1.1). It posts a job with executor shell to the NEW jobs route. Check: the answer is the NEW error code job.invalid and no job record exists.
- [ ] 2a.2 SECURITY. Add the hostile test TestJobCreateWithoutRunGrantDenied in internal/servertest/jobs_test.go. It calls POST jobs with an access token that lacks the NEW action unit:run-job. Check: the answer is 403 and an audit entry names the unit reference with secrets masked.
- [ ] 2a.3 SECURITY. Extend TestJobStoreConformance (NEW, see task 1.2) with a concurrent create case on both dialects. It races two creates for one unit ID. Check: exactly one create wins and the other gets the NEW error code job.busy on each dialect.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run usability check of the user path and fix error help text. Check: Developer completes create to write-back run from docs page alone, verified with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job calls. A person's step.
- [ ] 4.2 Run full definition of done: wiring, route, docs, usability, make check. Check: `make check` with zero findings after wiring; acceptance baseline uses access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` and BLOCKED by NEW jobs routes for job calls. A person's step. Confirmed by: CI (the agent runs the fast checks only).
