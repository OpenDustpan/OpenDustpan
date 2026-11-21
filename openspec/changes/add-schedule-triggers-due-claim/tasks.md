## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add conformance cases for ClaimDue: double claim, invalid slot and collapse of missed slots in pkg/trigger/triggertest/dispatcher.go. ClaimDue is NEW. It is an in-process test: BLOCKED by no route, because the harness calls the store port directly with no credential. Check: TestClaimDueDoubleStart fails on main
- [ ] 1.2 Add a case to the Dispatcher.Scheduled suite in pkg/trigger/triggertest that returns ErrInvalid for a foreign slot. It is an in-process test: BLOCKED by no route, because the suite calls the dispatcher directly with no credential. Check: TestDispatcherScheduledInvalidSlot fails on main
- [ ] 1.3 Add cron tests for parser, Next, presets and spread in the new cron package, by extending in place the existing file internal/cron/cron_test.go (main has it; add only the new cases). Check: TestSchedulePresetSpread fails on main

## 2. Build

- [ ] 2.1 Implement ClaimDue (NEW) in the trigger.Store port at pkg/trigger/ports.go:23 and in each store adapter under pkg/store as one conditional update with a 2 minute lease. Check: `go test -timeout 60m ./pkg/trigger/... ./pkg/store/...` passes
- [ ] 2.2 Extend Dispatcher.Scheduled at pkg/trigger/dispatcher.go:88 with the Next check (cron.Next is NEW), the run-as grant check, source kind schedule, lease and pause. BLOCKED by housekeeping-principals for the stored grant of the run-as principal. Check: `go test ./pkg/trigger/...` passes for two instances and one slot
- [ ] 2.3 Wire the durable schedule trigger.schedule (NEW) to Dispatcher.Scheduled each minute in the NEW file pkg/trigger/schedule.go. BLOCKED by run-triggers for the trigger record that the tick reads. Check: `go test ./pkg/durable/...` passes for one tick each minute and one catch-up run
- [ ] 2.4 Add the docs page for schedule triggers in the NEW file docs/triggers/schedule.md. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check the trigger routes declare their actions, and that the tick has no public route. Check: Route test lists the actions
- [ ] 4.2 Add an e2e step in the NEW file e2e/schedule_trigger_test.go: save a daily trigger with `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` with a personal token of a namespace administrator, then read the job with `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}`. Check: e2e step shows one plan job with source kind schedule.
- [ ] 4.3 Agent pre-run (human run-through in person-steps (add-schedule-triggers-due-claim 4.3)): Run a usability check of the docs page, with each error naming one command. Check: The persona follows the page alone. Record the transcript in the pull request body.
- [ ] 4.4 Run make check. Check: `make check` reports zero findings.
