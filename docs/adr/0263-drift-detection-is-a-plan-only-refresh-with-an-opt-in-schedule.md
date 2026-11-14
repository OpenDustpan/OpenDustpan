# 263. Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule

Status: accepted · 2026-10-20. Records decisions 25.4 and 25.5 of the owner of 18 October 2026. Decisions 8 to 20 are decided by the product manager under the owner's delegation of 20 October 2026. Answers RFC 25 questions 1 to 13 and open question 2 of ADR 161. Extends ADR 258 decision 1 and the phase table of ADR 100 decision 4. Narrows the scope of the built-in gate in ADR 258 decision 3 (decision 9)

Changed by:

- ADR 354 extends: triggers and drift schedules carry the delegation.
- ADR 358 extends decision 7: automatic remediation (auto-dedrift) is opt-in, and a namespace can always opt out.
.
## Context

[RFC 25](../rfc/0025-drift-detection.md) proposes drift detection: a
scheduled refresh-only run for each unit, a status in the inventory, and
actions on a drift. [ADR 258](0258-drift-remediation-is-a-plan-that-a-person-approves.md)
decided the remediation half. A drift run only plans, and a remediation is a
separate job that a person approves.

The owner decided on 18 October 2026:

- 25.4: a unit has the status `drifted`, and the context read API has a
  section for drift.
- 25.5: the drift schedule is off by default and opt in. The frequency comes
  from a list or from a custom cron expression.

[ADR 161](0161-one-context-read-api.md) asks in open question 2 whether drift
goes into the section `health` or into a section of its own.

The product manager decided the open questions of RFC 25 on 20 October 2026.
Decisions 8 to 20 hold those answers. The wire spelling of the statuses stays
with RFC 26. The slices stay in RFC 25.

## Decision

1. **A drift check is a job.** It has the operation `refresh` and one
   phase, `plan`. For OpenTofu it is a refresh-only plan. For the Pulumi
   CLI it is a refresh preview (ADR 258). It changes no resource and writes no state version. The phase table of
   ADR 100 decision 4 gets this row:

   | Executor | Operation | Phases |
   | --- | --- | --- |
   | runner | refresh (drift check) | plan |

   A plan phase never holds the unit (ADR 100 decision 10). So a drift check
   never blocks a change.
2. **The drift check sets the status `drifted`.** When a check finds a
   difference between the state and the real resources, the unit shows the
   status `drifted` in the inventory. A clean check clears it. The status
   comes from the drift result that the check stored. The other statuses
   and the wire values are the work of RFC 26.
3. **The server stores the drift result.** The result holds the addresses of
   the resources that differ. The unit keeps the result of its latest check.
   The server sets the time of the result from the end of the plan phase. It
   does not take the time from the runner, and the store refuses a time
   outside the run of the job.
4. **The context read API has a section of its own for drift.** Drift does
   not go into `health`. This answers open question 2 of ADR 161. A server
   that has no drift feature answers `unavailable` for the section, as
   ADR 161 says for any section whose feature is not there.
5. **A drift check starts on its own only when a person turned it on.**
   The setting is off by default. A unit has the setting, and a collection
   or a namespace can give a default. The frequency comes from a list or
   from a custom cron expression. With no setting, no drift check starts
   on its own.
6. **A change to `drifted` sends a notification.** It uses the model of
   ADR 145. The slice that sends it adds the type to the catalog of
   notification types.
7. **Out of scope: automatic remediation.** ADR 258 holds the rules.

The product manager decided decisions 8 to 20 under the owner's delegation of
20 October 2026. Each has its benchmark.

8. **A job says that it is a drift check by a `purpose` field.** The field
   `purpose` of the job spec has the value `drift-check`. The server fixes it
   at start. It selects the phase list, so the operation stays `refresh`.
   Benchmark: hosted IaC services run an assessment as a separate kind of
   run, not as a new operation.
9. **The built-in gate covers a job that changes something.** Decided by the
   product manager under the owner's delegation of 20 October 2026:
   `builtin/schedule-apply-needs-approval` covers a scheduled job that has a
   phase that writes state or changes resources (apply, destroy, a refresh
   that writes state). A drift check has a plan phase only and writes
   nothing, so it never enters `awaiting-approval`. Benchmark: change control
   applies to changes. A read-only check changes nothing.
10. **The statuses are `drifted`, `in-sync` and `unknown`.** RFC 26 decides
    the wire spelling.
11. **A failed check keeps the last result.** A check that fails or is
    canceled does not change the result or the time of the unit. The failure
    shows as a failed job. When another job writes a state version of the unit,
    the status becomes `unknown`. This starts when the job model writes state
    versions. A status that becomes `unknown` after missed periods comes with
    the schedule. Benchmark: keep the last assessment and show that the check
    failed.
12. **The runner reduces the plan to addresses and kinds.** The server reads
    no state. A spike must prove that a unit with client-side encryption still
    gives the addresses.
13. **OpenTofu first, then the Pulumi CLI.** The Pulumi CLI path starts after
    a separate e2e spike on the latest CLI.
14. **The schedule reuses the durable schedule of issue 909.** One cron parser
    serves both.
15. **The presets are `hourly`, `every-6-hours`, `daily` and `weekly`.** A
    custom cron expression has 5 fields, runs in UTC and has a minimum
    interval of 1 hour. The first check of a unit is at an offset from a hash
    of the unit ID inside the period. The parser uses the standard library,
    adds no dependency and lives with issue 909.
16. **The schedule of a unit pauses after 5 failed checks in a row.** The
    server sends a notification when it pauses. Benchmark: scheduled CI
    workflows are disabled instead of failing for ever.
17. **Actions.** A manual check needs the action `unit:run-plan`. A change to
    the setting needs the settings action of the object. This adds no action.
18. **The action to ignore a drift waits for remediation.** ADR 258 decision 5
    holds the rule.
19. **The context section lists 100 addresses and a `truncated` flag.** It
    shows no attribute name.
20. **A drift check keeps the default state lock.** The check does not use
    `-lock=false`. A check that cannot get the lock fails, and decision 11
    keeps the last result. Benchmark: hosted assessments do not run while a
    run is active.

## Consequences

- The detection slices can ship with no remediation. A drift check has no
  apply phase, so the built-in gate does not cover it (decision 9) and
  ADR 258 decision 3 has nothing to hold in it. A test must show that a
  scheduled drift check never enters `awaiting-approval`.
- `Spec.Changes()` is false for a drift check, and `Phases` takes the
  purpose. The checker needs two new fields in `job.StartRequest`: the
  purpose and the source kind `schedule`. Issue 1165 adds the source kind.
- ADR 205 decision 4 and ADR 226 decision 7 say that a `refresh` has no
  plan phase. That stays true for a manual refresh and is not true for a
  drift check.
- The glossary gets the terms **Drift**, **Drift check**, **Drift result**
  and **Drift schedule**.
- A feature that a user can see ships with its docs page (ADR 214). Each
  slice of RFC 25 carries its page.
