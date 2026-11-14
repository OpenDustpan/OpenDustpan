## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing browser test for run timeline against suite server. Check: Browser test run-page-timeline shows phase order for a plan job with fixed fixture.
- [ ] 1.2 Add failing browser test for live log polling. Check: Browser test run-page-log appends masked chunks, shows last poll time, and recovers after a failed poll.
- [ ] 1.3 Add failing browser test for masked plan diff. Check: Browser test run-page-plan shows masked diff and shows plan pending before store.
- [ ] 1.4 Add failing browser test for approval from run page. Check: Browser test run-page-approval records approval, shows resubmit without duplicate change and late submit refusal.
- [ ] 1.5 Add failing browser test for render and refresh targets. Check: Browser test run-page-render meets 2 second first render and 5 second refresh targets.

## 2. Build and docs

- [ ] 2.1 Wire run page to job, log, plan and approval routes from dependencies. Check: `GET /api/dustpan/v1/namespaces/{}/jobs/{}` returns phases, gates and approvals for the page.
- [ ] 2.2 Write docs page for the run page path. Check: Docs page runbook commands run as written against suite server.
- [ ] 2.3 Run usability check of full run page path. Check: Developer completes timeline, log, diff and approval from docs page alone.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Verify wiring, route actions and public API use. Check: Route declares authorization actions for job read, log read, plan read and approval write.
- [ ] 4.2 Run e2e step with the real server and a real runner job. Check: BLOCKED by the job routes of add-job-service-routes-start-1 and the approval route of policy-approvals; once they land, browser tests call `GET /api/dustpan/v1/namespaces/{}/jobs/{}` with a personal token of a principal that holds the read action.
- [ ] 4.3 Run make check with zero findings. Check: `make check` reports zero findings.
