## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server tests for create, single ClaimJob, cancel, and forward-only advance scenarios. Check: TestJobScenarios_CreateAdvance and TestJobScenarios_Cancel fail on main, pass after, with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` for the read baseline.
- [ ] 1.2 Add SQL adapters for job records by stable job ID with fence-checked writes. Check: TestJobStoreConformance fails on main, passes after, with fence-checked writes on pkg/job records.
- [ ] 1.3 Edit the baseline schema of each dialect in place for job tables and the active-job unique index. Check: Baseline schema edit in each dialect passes sqlc diff for job tables and the active-job unique index.

## 2. Wiring and docs

- [ ] 2.1 Wire NEW tool-neutral jobs routes GET and POST /api/dustpan/v1/namespaces/{namespace}/jobs and the NEW cancel route with the NEW action unit:run-job and audit entries, plus openapi regen. Check: NEW POST /api/dustpan/v1/namespaces/{namespace}/jobs creates a job with access token holding the NEW action unit:run-job; BLOCKED by NEW jobs routes for the create call itself, read baseline on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` with access token.
- [ ] 2.2 Wire write-back advance under the server executor identity with idempotent phase reports. Check: Write-back advance verified with access token on the NEW jobs routes; BLOCKED by NEW jobs routes, read baseline on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` with access token.
- [ ] 2.3 Write docs page for jobs: create, list, cancel, phases, and errors. Check: Docs page commands run as written in a fresh install, verified with access token reads on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job create examples.
- [ ] 2.4 Measure job create latency target and record the result. Check: 500 ms p95 create to queued measured in internal/servertest with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` as the read baseline; BLOCKED by NEW POST /api/dustpan/v1/namespaces/{namespace}/jobs for the create call.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run usability check of the user path and fix error help text. Check: Developer completes create to write-back run from docs page alone, verified with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job calls.
- [ ] 4.2 Run full definition of done: wiring, route, docs, usability, make check. Check: `make check` with zero findings after wiring; acceptance baseline uses access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` and BLOCKED by NEW jobs routes for job calls.
