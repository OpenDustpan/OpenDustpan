## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for the 7-day lead notice in NEW pkg/namespace/namespacetimer_test.go. Check: NEW TestLeadNoticeRecordedSevenDaysBeforeStep fails on main; BLOCKED by the read-namespace route from part 1 (access token).
- [ ] 1.2 Add failing test for the durable timed step in NEW pkg/namespace/namespacetimer_test.go. Check: NEW TestTimedStepRunsFromDurableSchedule fails on main; BLOCKED by the read-namespace route from part 1 (access token).

## 2. Implementation

- [ ] 2.1 Implement the durable schedule in NEW pkg/namespace/namespacetimer.go. Check: NEW ScheduleTimedStep runs the step from the stored schedule with one worker claim.
- [ ] 2.2 Implement the lead notice record and send in NEW pkg/namespace/namespacetimer.go. Check: NEW SendLeadNotice records one notice per step with no duplicate.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the schedules to the worker and show state and due time on the read-namespace route. Check: `GET /api/dustpan/v1/namespaces/{namespace}` with an access token shows the due time; BLOCKED by the read-namespace route from part 1.
- [ ] 4.2 Add the docs page for lead notices and timed steps. Check: Docs page commands run as written.
- [ ] 4.3 Run the usability check of the operator path. Check: Operator completes the path from the docs page alone.
- [ ] 4.4 Run the full check suite. Check: `make check` passes with zero findings.
