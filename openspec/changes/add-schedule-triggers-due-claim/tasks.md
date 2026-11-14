## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add conformance cases for ClaimDue: double claim, invalid slot and collapse of missed slots. Check: TestClaimDueDoubleStart fails on main
- [ ] 1.2 Add a case to the Dispatcher.Scheduled suite in pkg/trigger/triggertest that returns ErrInvalid for a foreign slot. This is an in-process test with the stored grant of the namespace service principal; it calls no route and uses no token. Check: TestDispatcherScheduledInvalidSlot fails on main
- [ ] 1.3 Add pkg/cron tests for parser, Next, presets and spread. Check: TestSchedulePresetSpread fails on main

## 2. Build

- [ ] 2.1 Implement ClaimDue in each store adapter as one conditional update with a 2 minute lease. Check: `go test ./pkg/trigger/... ./pkg/store/...` passes
- [ ] 2.2 Extend Dispatcher.Scheduled with the Next check, last-saver read check, source kind schedule, lease and pause. It runs in process as the namespace service principal; no HTTP route and no token are used. Check: `go test ./pkg/trigger/...` passes for two instances and one slot
- [ ] 2.3 Wire the durable schedule trigger.schedule to Dispatcher.Scheduled each minute. The path is in process through pkg/durable with the stored grant of the namespace service principal; no HTTP route and no token. Check: `go test ./pkg/durable/...` passes for one tick each minute and one catch-up run
- [ ] 2.4 Add the docs page for schedule triggers. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check the trigger routes under `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` declare their actions, and that the tick has no public route. Check: Route test lists the actions
- [ ] 4.2 Add an e2e step: save a daily trigger with `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` with a personal token of a namespace administrator, then read the job with `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}`. The tick itself runs in the server with the stored grant of the namespace service principal. Check: e2e step shows one plan job with source kind schedule
- [ ] 4.3 Run a usability check of the docs page, with each error naming one command. Check: The persona follows the page alone
- [ ] 4.4 Run make check. Check: `make check` reports zero findings
