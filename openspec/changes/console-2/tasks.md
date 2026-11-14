## 1. Failing tests first

- [ ] 1.1 Add failing browser test for run timeline against suite server. Check: Browser test run-page-timeline shows phase order for a plan job with fixed fixture.
- [ ] 1.2 Add failing browser test for live log with masked secrets. Check: Browser test run-page-log shows masked chunks appended with fixed chunk sequence and bounded wait.
- [ ] 1.3 Add failing hostile test for secret split across chunk boundary. Check: Hostile test log-chunk-boundary-split fails when masking joins only one chunk.
- [ ] 1.4 Add failing browser test for approval from run page. Check: Browser test run-page-approval records approval and leaves wait state, plus refusal scenario for missing approve action.
- [ ] 1.5 Add failing browser test for unknown job ID. Check: Browser test run-page-not-found shows 404 for unknown and cross-namespace job ID and 403 for read without read action.

## 2. Build and docs

- [ ] 2.1 Wire run page to job, log, plan and approval routes from dependencies. Check: GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job} returns phases, gates and approvals.
- [ ] 2.2 Write docs page for the run page path. Check: Docs page runbook commands run as written against suite server.
- [ ] 2.3 Run usability check of full run page path. Check: Developer completes timeline, log, diff and approval from docs page alone.

## 3. Definition of done

- [ ] 3.1 Verify wiring, route actions and public API use. Check: Route declares authorization actions for job read, log read, plan read and approval write.
- [ ] 3.2 Run e2e step with real server and real runner job. Check: Browser tests run against suite server over TLS on SQLite and PostgreSQL.
- [ ] 3.3 Run make check with zero findings. Check: `make check` reports zero findings.
