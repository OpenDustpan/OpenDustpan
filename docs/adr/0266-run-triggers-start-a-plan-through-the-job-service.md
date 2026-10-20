# 266. Run triggers: three kinds, a plan only, started through the job service

Status: accepted · 2026-10-20. Records decisions 24.1, 24.3 and 6.1 of the owner, and the scope and the acceptance criteria that the owner wrote in issue 909. Uses ADR 153 decision 2, ADR 258 decision 3 and ADR 263 decisions 14 and 15. Answers no open question of RFC 24 beyond these decisions

## Context

[RFC 24](../rfc/0024-run-triggers-and-orchestration.md) proposes that runs
start on their own. Other products start a run from a schedule, from a push to
a review request, and from a change of another unit. Today a person or a CI
pipeline starts each run.

The owner decided in issue 909:

- 24.1 (18 October 2026): the first trigger kinds are push to a review
  request, schedules, and unit-follows-unit triggers.
- 24.3: unattended applies pass approval rules.
- 6.1: the runner pool is the only routing key for now.

The owner also wrote the scope and the acceptance criteria of issue 909. A
push to a review request starts a plan job for matching units. A schedule
fires once per period across several instances. A unit B with `follows: A`
starts a plan after A applies, and the server refuses a cycle at save. Each start goes
through gates and approval rules. The feature ships with a docs page
([ADR 214](0214-a-user-facing-feature-ships-with-its-docs-page.md)).

[ADR 258](0258-drift-remediation-is-a-plan-that-a-person-approves.md)
decision 3 makes the apply phase of a scheduled job wait for the approval of
a person. [ADR 263](0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md)
decisions 14 and 15 say that the drift schedule and the triggers share the
durable schedule and one cron parser.

## Decision

1. **A trigger belongs to one unit and has one kind.** The unit is the unit
   that the trigger starts. The kinds are `review-request-push`, `schedule`
   and `follows` (24.1). A unit is a stack or a workspace of any tool, and the
   trigger model is tool-neutral (ADR 13).
2. **A trigger starts a plan job.** The acceptance criteria name a plan for
   each kind. The port that a trigger uses to start a job can start a plan
   and nothing else.
3. **A trigger starts a job through the job service.** It has no path of its
   own. The gates, the approval rules, the change freezes, the manual locks
   and the limits of open jobs apply to the job as to any job.
4. **An unattended start has no exemption (24.3).** A job that a trigger
   starts without a person passes the approval rules like any other job. A
   scheduled job that has a phase that writes state or changes resources
   waits for the approval of a person at the built-in gate of ADR 258
   decision 3. No trigger turns that gate off.
5. **The runner pool is the only routing key (6.1).** A trigger has no pool
   and no label. The job takes the pool of the unit settings.
6. **A schedule has a frequency from a list or a custom cron expression.**
   The list is `hourly`, `every-6-hours`, `daily` and `weekly`. A custom
   expression has 5 fields, runs in UTC and has a minimum interval of 1
   hour. The first run of a unit is at an offset from a hash of the unit ID
   inside the period. One parser serves the triggers and the drift schedule
   (ADR 263 decisions 14 and 15). It uses the standard library, adds no
   dependency and lives in `pkg/cron`.
7. **A schedule fires once per period across several instances.** A schedule
   trigger uses the durable schedule (ADR 263 decision 14), so that of
   several instances one starts the job of a period.
8. **A `follows` trigger names a source unit.** After the source applies, a
   plan of the unit starts. The source is a unit reference, so a unit of one
   tool can follow a unit of another tool.
9. **The server refuses a cycle of `follows` triggers at save.** The error
   names the units of the loop in order.
10. **A `follows` trigger is a declared dependency edge.** ADR 153 decision 2
    says that a feature that declares a link between units adds its own
    kind of edge, with the evidence `declared`. The `follows` trigger does
    that. Such an edge blocks a delete of the source like any declared edge
    (ADR 153 decision 5).
11. **Out of scope.** The TFE-compatible view of run triggers, and the run
    order of units, are separate issues.

## Consequences

- This change adds the ports (`pkg/trigger`), their conformance suite
  (`pkg/trigger/triggertest`) and the cron parser (`pkg/cron`). It adds no
  adapter, no SQL and no route.
- RFC 24 holds the open questions: what counts as an apply of the source, the
  principal of an unattended start, the source kind of the job, how a cron
  expression maps onto the durable schedule, the match of a review request
  push, the limits, and the other trigger kinds.
- Decision 2 keeps the first slices free of an apply that no person approves.
  A later decision can allow an apply from a trigger. It must keep the gate of
  decision 4.
- `job.StartRequest` needs two fields before the first start from a trigger:
  the source kind of the start, and the ID of the trigger. Issue 1165 adds the
  source kind.
- The glossary gets the terms **Trigger** and **Follows trigger**.
- A feature that a user can see ships with its docs page (ADR 214). Each slice
  of RFC 24 carries its page.
