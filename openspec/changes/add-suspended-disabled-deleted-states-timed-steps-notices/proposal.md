---
version: 1
---

## Problem

Operator keeps namespaces on the timed path from suspended to disabled to deleted. The operator needs a 7-day warning before each automatic step and a schedule that survives a restart.

## Evidence

- Main holds a durable package for background work. Proof: pkg/durable
- Main holds an audit chain schedule test that uses a durable store. Proof: TestAuditChainChecksOnceAtStartWithADurableStore at pkg/dustpan/auditchain_schedule_internal_test.go:367

## Fix boundary

- Add durable schedules for the timed namespace steps.
- Add one 7-day lead notice record per timed step with no duplicate.

Out of scope:

- Request filter behavior for suspended namespaces
- Batched purge 30 days after deleted
- Namespace backup, restore, and fence behavior

## Check

- Add failing test for the 7-day lead notice in NEW pkg/namespace/namespacetimer_test.go. Check: NEW TestLeadNoticeRecordedSevenDaysBeforeStep fails on main; BLOCKED by the read-namespace route from part 1 (access token).
- Add failing test for the durable timed step in NEW pkg/namespace/namespacetimer_test.go. Check: NEW TestTimedStepRunsFromDurableSchedule fails on main; BLOCKED by the read-namespace route from part 1 (access token).
- Implement the durable schedule in NEW pkg/namespace/namespacetimer.go. Check: NEW ScheduleTimedStep runs the step from the stored schedule with one worker claim.
- Implement the lead notice record and send in NEW pkg/namespace/namespacetimer.go. Check: NEW SendLeadNotice records one notice per step with no duplicate.
- Wire the schedules to the worker and show state and due time on the read-namespace route. Check: `GET /api/dustpan/v1/namespaces/{namespace}` with an access token shows the due time; BLOCKED by the read-namespace route from part 1.
- Add the docs page for lead notices and timed steps. Check: Docs page commands run as written.
- Run the usability check of the operator path. Check: Operator completes the path from the docs page alone.
- Run the full check suite. Check: `make check` passes with zero findings.

Depends on:

- add-suspended-disabled-deleted-states-namespace-routes
- `add-suspended-disabled-deleted-states-namespace-routes` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a durable package for background work that survives the stop of an instance. (`pkg/durable/durabletest/durabletest_test.go:35 (TestMemSchedules)`)
- Main has notification tests for changed notices. (`pkg/notify/notify_test.go:940 (TestSinkChangedNotice)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this part change the request filter for suspended namespaces? No. The roadmap scope for this part keeps the request filter as set by part 1.
- Does this part add the batched purge after deleted? No. The last part adds it.
- Can an operator change the 7-day lead time? No. It is a fixed constant in this part.
- Does a timed step wait for its lead notice? No. The step runs at its due time and the audit entry names the missed notice.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
