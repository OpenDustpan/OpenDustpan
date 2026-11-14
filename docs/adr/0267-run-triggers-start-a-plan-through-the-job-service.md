# 267. Run triggers: three kinds, a plan only, started through the job service

Status: accepted · 2026-10-20. Records decisions 24.1, 24.3 and 6.1 of the owner, and the scope and the acceptance criteria that the owner wrote in issue 909. Decisions 9 to 23 are decided by the product manager under the owner's delegation of 20 October 2026. Accepts RFC 24 and answers its questions 1 to 12. Uses ADR 153 decisions 2 and 5, ADR 213 decision 1, ADR 258 decision 3 and ADR 263 decisions 14 and 15. Decision 1 of ADR 213 extended by decision 14

Changed by:

- ADR 307 extends decision 8: the TFE-compatible view of run triggers.
- ADR 273 changes decision 14: the last saver needs unit:read-outputs.
- ADR 299 changes decisions 14 and 19: one credential rule for every start of
  a plan.
- ADR 310 changes decision 22: the run order.
- ADR 338 changes decision 14: the permission of the namespace service principal is a stored grant.
- ADR 369 changes decision 14: a triggered job acts and reads as the run-as
  principal of its trigger.

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

### Decisions of the owner

1. **A trigger belongs to one unit and has one kind.** The unit is the unit
   that the trigger starts. The kinds are `review-request-push`, `schedule`
   and `follows` (24.1). A unit is a stack or a workspace of any tool, and the
   trigger model is tool-neutral (ADR 13).
2. **A trigger starts a job through the job service.** It has no path of its
   own. The gates, the approval rules, the change freezes, the manual locks
   and the limits of open jobs apply to the job as to any job.
3. **An unattended start has no exemption (24.3).** A job that a trigger
   starts without a person passes the approval rules like any other job. A
   scheduled job that has a phase that writes state or changes resources
   waits for the approval of a person at the built-in gate of ADR 258
   decision 3. No trigger turns that gate off.
4. **The runner pool is the only routing key (6.1).** A trigger has no pool
   and no label. The job takes the pool of the unit settings.
5. **A schedule has a frequency from a list or a custom cron expression.**
   The list is `hourly`, `every-6-hours`, `daily` and `weekly`. A custom
   expression has 5 fields, runs in UTC and has a minimum interval of 1
   hour. The first run of a unit is at an offset from a hash of the unit ID
   inside the period. One parser serves the triggers and the drift schedule
   (ADR 263 decisions 14 and 15). It uses the standard library, adds no
   dependency and lives in `pkg/cron`.
6. **A schedule fires once per period across several instances.** A schedule
   trigger uses the durable schedule (ADR 263 decision 14), so that of
   several instances one starts the job of a period.
7. **A `follows` trigger names a source unit, and the server refuses a cycle
   at save.** After the source applies, a plan of the unit starts. The source
   is a unit reference, so a unit of one tool can follow a unit of another
   tool.
8. **Out of scope.** The TFE-compatible view of run triggers, and the run
   order of units, are separate issues.

### Decisions of the product manager

9. **A trigger starts a plan and nothing else.** The acceptance criteria name
   a plan for each kind. The port that a trigger uses to start a job can start
   a plan and nothing else. No trigger starts an apply in these slices (RFC 24
   question 5). A later decision can allow it. It must keep the gate of
   decision 3, and the scope of the schedule gate must then cover the source
   kind `trigger` too (decision 15).
10. **The error of a cycle names the path.** It names the units of the loop
    in order. The adapter can add the names that the tool of each unit uses.
11. **A `follows` trigger is a declared dependency edge.** ADR 153 decision 2
    says that a feature that declares a link between units adds its own kind
    of edge, with the evidence `declared`. The `follows` trigger does that.
    Such an edge blocks a delete of the source like any declared edge (ADR 153
    decision 5). A forced delete is the only way past a follower edge.
12. **"A applies" uses only what the server can see (question 1).**
    - For the Pulumi CLI, an update of the kind `update` counts. A refresh, a
      preview and a destroy do not.
    - For OpenTofu over the state backend, any final state version counts. The
      request does not say which command wrote it. The docs page says so.
    - A run on a workstation counts. The idempotency is per state version,
      and the limits of decision 20 apply.
13. **A `follows` trigger has a field `when` (question 2).** The default is
    `applied`: after each apply of the source. The value `outputs-changed`
    comes in a later slice.
14. **The principal of a start that no person makes (question 3).**
    - The start runs as the namespace service principal. That principal holds
      only the action that starts a plan.
    - For a job that a trigger starts, check 2 of ADR 213 decision 1 uses
      the current grants of the trigger's last saver: `unit:read` on the
      source, plus `unit:read-secrets` for a secret output. Check 1 (the
      output-sharing rule) still applies.
    - The server checks at each read and fails closed. A saver that is
      missing, deleted or disabled means deny. The server never rebuilds the
      admin flag. It treats the flag as false.
    - The namespace service principal that starts the plan gets no read
      action. The job carries the trigger ID, so that the read handler can
      find the saver.
    - If that person loses access, the trigger needs a new save, and the
      console says so.
    - The trigger stores its last saver. The audit entry of each start names
      the trigger and the saver.
    - At save, the saver must hold `unit:read` on the source.
15. **The source kinds (question 4).** A `schedule` trigger records the source
    kind `schedule`. A push records `forge`. A `follows` start records the new
    source kind `trigger`. A manual fire keeps the source kind of the caller
    (`api`) and records the trigger ID.
16. **A trigger never applies in these slices (question 5).** See decision 9.
17. **Cycles (question 6).** The server refuses a cycle at save. An adapter
    serializes the trigger writes of one namespace, so that two saves at the
    same time cannot skip the check. A depth guard at run time refuses a chain
    that is deeper than a fixed limit, for the rows of another version and for
    a restore.
18. **One tick for all schedule triggers (question 7).** One durable
    schedule, `trigger.schedule`, ticks each minute. The store keeps the next
    slot of each trigger. After an outage, at most one catch-up run starts, for
    the latest missed slot. `ClaimDue` checks that a slot is a time that the
    schedule gives.
19. **A push to a review request (question 8).**
    - A unit matches when the review request targets the repository and the
      branch of its code location.
    - The list `paths` of the trigger defaults to the working directory of the
      code location of the unit.
    - A push from a fork starts nothing.
    - A newer push replaces the older plan.
20. **Limits (question 9).** A unit has at most 20 triggers. A source has at
    most 50 followers. An administrator can change both.
21. **No follow across namespaces (question 10).** A write in one namespace
    carries the fence of that namespace only.
22. **Run order is a separate RFC (question 11).**
23. **No other kinds now (question 12).** A team that wants a git tag or an
    inbound webhook calls the REST API from its own automation.

## Consequences

- This change adds the ports (`pkg/trigger`), their conformance suite
  (`pkg/trigger/triggertest`) and the cron parser (`pkg/cron`). It adds no
  adapter, no SQL and no route.
- This ADR accepts RFC 24. Its slices stay in the RFC.
- Decision 9 keeps the first slices free of an apply that no person approves.
  A later decision can allow an apply from a trigger. It must keep the gate of
  decision 3.
- `job.StartRequest` needs two fields before the first start from a trigger:
  the source kind of the start, and the ID of the trigger. Issue 1165 adds the
  source kind. The scope of the built-in gate of ADR 258 decision 3 covers
  `schedule` now, and it must cover `trigger` before a trigger can apply.
- The glossary gets the terms **Trigger** and **Follows trigger**.
- A feature that a user can see ships with its docs page (ADR 214). Each slice
  of RFC 24 carries its page.
