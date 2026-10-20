---
rfc: 24
title: Run triggers and orchestration between units
status: accepted
created: 2026-10-10
adr: 153, 267
---

# RFC 24. Run triggers and orchestration between units

Status: accepted
([ADR 267](../adr/0267-run-triggers-start-a-plan-through-the-job-service.md)).
The ADR records the decisions of the owner and the answers of the product
manager. This text keeps the questions, with the options and a recommendation
for each. The Outcome section lists the answers.
The ports are in `pkg/trigger` and the cron parser is in `pkg/cron`. No
adapter, no route and no job code exists yet.

## Problem

RFC 6 says triggers come "later, one at a time". Competitors trigger runs from
schedules, git pushes filtered by path, git tags, inbound webhooks and
upstream changes. HCP Terraform offers run triggers between workspaces. Spacelift
starts a dependent stack only when a referenced output changed and cancels
everything below a failure. env zero runs a workflow graph with "run from
here". Terramate and Atmos Pro run only the units that a change affects.

A team wants three things: a plan for each push to a review request, a plan on
a timetable, and a plan of one unit after another unit applies.

## What the ADRs decide

| Source | Decision |
| --- | --- |
| ADR 267 decision 1 | A trigger belongs to one unit. Its kind is `review-request-push`, `schedule` or `follows`. |
| ADR 267 decisions 2 and 9 | A trigger starts a plan, through the job service. |
| ADR 267 decision 3 and ADR 258 decision 3 | An unattended start has no exemption. A scheduled job that writes waits for a person at the built-in gate. |
| ADR 267 decision 4 | The runner pool is the only routing key. |
| ADR 267 decision 5 and ADR 263 decision 15 | The frequency is a preset or a cron expression of 5 fields in UTC, with a minimum interval of 1 hour. |
| ADR 267 decision 6 and ADR 263 decision 14 | A schedule uses the durable schedule, and fires once per period across instances. |
| ADR 267 decision 7 | A `follows` trigger names a source unit. The server refuses a cycle at save. |
| ADR 267 decision 11 and ADR 153 decision 2 | A `follows` trigger is a declared dependency edge. |
| ADR 213 decision 1 | A job reads the outputs of another unit with a sharing rule and the grants of its starter. |
| ADR 100 decision 10 | A plan phase never holds the unit. |

## The proposed model

### Triggers

| Kind | Event | Idempotency key | What it starts |
| --- | --- | --- | --- |
| `schedule` | The durable schedule claims a slot | `ScheduleKey(trigger, slot)` | A plan of the unit |
| `follows` | A final state version of the source unit | `UpstreamKey(trigger, version)` | A plan of the unit |
| `review-request-push` | A push to a review request (waits for the Forge port) | The trigger, the review request and the head commit | A plan of the unit, at that commit |
| (any) | A person fires the trigger now | `ManualKey(trigger, minute)` | A plan of the unit |

The user text uses each tool's own words: "plan" for OpenTofu, and "preview" for
the Pulumi CLI. A trigger belongs to a unit, and the unit is a stack or a
workspace.

### The ports (`pkg/trigger`)

- `Store` keeps the triggers: `Put` (create or replace), `Get`, `List`,
  `Followers` and `Delete`. A write checks the fence and appends its audit
  entry in one transaction. `Put` applies the shared rule `CheckCycle` in the
  same transaction.
- `Starter` starts one plan through the job service. It is the only way for a
  trigger to start a job, and it has no method for an apply.
- `Dispatcher` fires triggers: `Scheduled`, `Upstream` and `Manual`. The
  kind `review-request-push` joins when the Forge port exists.
- `pkg/trigger/triggertest` holds the conformance suite. A memory reference
  runs it today. A SQL adapter must run it too.

### The cron parser (`pkg/cron`)

`cron.Parse` reads 5 fields (minute, hour, day of month, month, day of week).
It accepts `*`, lists, ranges, steps and numbers. It refuses names, macros,
`?`, `L`, `W` and `#`. It refuses an expression that never runs and one that
can run twice within an hour. `Schedule.Next` returns the next run in UTC.
`cron.Expression` turns a preset into an expression, with an offset from a hash
of the unit ID. The package uses only the standard library.

### Defense in depth (ADR 250)

Every input of a trigger comes from outside the process: the API body, the
cron text, the event of a forge, and the rows of a store from another version.

| Step | Layer in this design |
| --- | --- |
| Check at the boundary | `Trigger.Validate`, `ID.Validate` and `cron.Parse` use allowlists. They reject a bad value and never change it. |
| Contain the value | The cron parser reads bytes, not a regular expression. Error texts quote at most 32 bytes. Idempotency keys use only IDs that passed the check. |
| Limit the effect | The Starter starts a plan only. `MaxBytes` bounds the cron text. `Next` has a fixed bound on its work. Questions 7 and 9 add the limits on fan-out. |
| Check authorization at the point of use | The caller of `Put` checks the actions at save. The dispatcher uses a principal that can only start a plan (question 3). The job service checks the actions again at each start. |
| Fail closed | A zero `Schedule` never runs. A trigger that is off starts nothing. A job that the service refuses is an error of that start and never a retry that skips a rule. |

The tests in this change send hostile input to the steps check, contain,
limit and fail closed, each alone. The step of authorization has its tests with
the slice that adds the caller.

## Questions

Each question has the options and a recommendation. The product manager decides.

1. **What counts as "A applies", and where the event comes from.**
   - (a) A final state version that a run of the tool wrote, whatever started
     the run (a job, a workstation or a pipeline). The tool package sends the
     event only for a run that applies a change. A refresh, a plan and a
     destroy send nothing.
   - (b) A succeeded apply job only.

   *Recommendation: (a).* The acceptance criteria say "after A applies", and
   teams apply from a workstation too. The tool packages already see the final
   version (`job.StateVersion` with `Final`). The server knows an apply by that
   version, so the port carries a version ID and not a job ID. Each tool
   package decides which run counts: a Pulumi `update`, and an OpenTofu run
   that applies. This needs the slice that writes the final state versions.
2. **When a `follows` trigger fires.**
   - (a) After each apply of the source.
   - (b) Only when the outputs of the source changed. This needs a digest of
     the root outputs of each final version.
   - (c) A field `when` with both values and the default (a).

   *Recommendation: (c), with (a) first.* The acceptance criteria say "after A
   applies". A comment on issue 909 proposes (b) for the first slice. The
   criteria win until the product manager says otherwise. (b) comes in a later
   slice, when ADR 213 decision 1 and the final state versions give the digest.
3. **The principal of a start that no person makes, and who may save a
   trigger.** A schedule, a `follows` trigger and a push have no person at
   the start.
   - (a) The principal that saved the trigger. Its grants can change or end
     after the save.
   - (b) A service principal of the namespace with the one action that starts a
     plan.
   - (c) A service principal for each trigger.

   *Recommendation: (b).* It has the smallest permission and it shows in the
   audit log as the trigger principal. At save, the caller needs the settings
   action of the unit and the action that starts a plan on it. For a
   `follows` trigger the caller also needs `unit:read` on the source, so that
   a person cannot learn when a unit applies without the right to read it.
   The job service checks the actions of the principal again at each start.
4. **The source kind that the job records, and the scope of the gates.** The
   facts of a gate have no source kind for a `follows` start.
   - (a) A trigger uses `schedule`, `forge` or `api` by its kind. A `follows`
     start uses `api`.
   - (b) A new source kind `trigger` for the `follows` kind. A `schedule`
     trigger uses `schedule`. A push uses `forge`.
   - (c) One new source kind `trigger` for all three.

   *Recommendation: (b).* The built-in gate of ADR 258 decision 3 covers
   `schedule`, so the schedule trigger fits it with no change. A `follows`
   start needs a name that a rule can match. The job also records the ID of
   the trigger, for the audit log and for question 6.
5. **Can a trigger start an apply?**
   - (a) Never. A person applies after the plan.
   - (b) An opt-in field on the trigger. The built-in gate then covers every
     start that no person makes. An installation administrator holds the
     switch of the gate (ADR 258 decision 3).
   - (c) Per trigger, with the approval rules as the only control.

   *Recommendation: (a) for the slices of this RFC.* ADR 267 decision 9
   already fixes a plan for the first slices. Decide (b) with real use. (c) is
   against ADR 258 decision 3.
6. **How the server finds and reports a cycle.**
   - (a) Refuse a cycle at save. The error names the path.
   - (b) Allow it and stop at run time with a depth limit.
   - (c) Both.

   *Recommendation: (c).* ADR 267 decision 7 fixes (a). `CheckCycle` is the one
   rule, and each adapter calls it in the transaction of `Put`, with a lock
   that serializes the writes of one namespace, so two saves at once cannot each
   pass. A depth
   counter in the origin of the job is a second layer. If the rows of another
   version, or a restore, make a loop, a start refuses a chain deeper than a
   fixed limit. The slice proposes the number and the report in the console.
7. **How a cron expression maps onto the durable schedule.** `durable`
   schedules have a fixed list at start and a fixed interval. Triggers appear
   at run time, and a cron expression has no fixed interval.
   - (a) One durable schedule for each trigger, added at run time.
   - (b) One durable schedule, `trigger.schedule`, that ticks each minute. The
     store holds the next slot of each trigger. A tick claims the due triggers
     with one conditional update for each trigger, and starts the job with the
     key `ScheduleKey(trigger, slot)`.
   - (c) Give `durable.ScheduleSpec` a function for the next time.

   *Recommendation: (b).* It follows ADR 263 decision 14 and changes nothing in
   `durable`. The conditional update gives one claim for each slot across
   instances. A slot that no instance ran collapses with the next one, so
   an outage gives one start and no burst. The minimum interval of 1 hour keeps
   the load small. The drift schedule can use the same machinery. The schedule
   slice adds the port `ClaimDue` with its conformance cases first.
8. **How a push to a review request matches units.** This waits for the Forge
   port. Two parts are open: the match, and the code of a fork.
   - (a) A unit matches when the review request targets the repository and the
     branch of its code location, and a push changes a path in the optional
     list `paths` of the trigger.
   - (b) Every unit of the repository.
   - (c) Only units that list explicit paths.

   *Recommendation: (a).* A newer push to one review request replaces the
   plan of the older one for the same trigger. A plan runs code of the
   author (providers, external data sources and scripts) with the credentials of the
   unit profile. So the first slice refuses a push from a fork. The source
   policy (`pkg/source`) decides about forks later.
9. **Limits.**
   - (a) A fixed cap on triggers for each unit and on followers for each
     source, and the open-job limit of the job service.
   - (b) Only the open-job limit.

   *Recommendation: (a), with 20 triggers for each unit and 50 followers for
   each source.* An installation administrator can change both. A source with
   many followers would else start many plans from one apply. The slice that
   adds the caps adds their conformance cases.
10. **A `follows` trigger across namespaces.**
    - (a) The same namespace only.
    - (b) Allow it with the consent of both namespaces (the question of RFC 15).

    *Recommendation: (a).* A write in one namespace carries the fence of that
    namespace only. `Trigger.Validate` refuses a source in another namespace
    today, so a change to (b) is a visible decision.
11. **Run order and "stop below a failure".** Competitors run units in order,
    stop below a failure and offer "run from here".
    - (a) A separate RFC, after `follows` triggers are in use. A chain of
      `follows` triggers gives an order, and a failed apply sends no event.
    - (b) Add order to this RFC.

    *Recommendation: (a).* The issue puts run order in a separate issue.
12. **Other kinds: git tag and inbound webhook.**
    - (a) Not now. A team that wants them calls the REST API from its own
      automation, with its own principal (ADR 17). The job then has the source
      kind of its caller.
    - (b) Add them as kinds.

    *Recommendation: (a).* ADR 258 decision 4 uses the same route for automatic
    remediation. The decision of the owner names three kinds.

## Slices after this design

Each slice is one thin end-to-end change. It carries its e2e step first and
its docs page (ADR 214). The follow-up issues carry the dependencies. All of
them need the job claimer (issue 1162) and the job service (issue 1165).

1. **Save and fire a trigger on demand.** `Store` over SQL in both dialects,
   the routes to create, read, list and delete a trigger, the cycle refusal,
   and `Manual` through the job service.
2. **A `follows` trigger starts a plan after an apply.** The `Upstream` call
   from the tool packages, OpenTofu first, with an e2e step that drives the
   real CLI. The declared dependency edge. Needs the final state versions.
3. **A schedule trigger.** `ClaimDue`, the durable tick, the spread and the
   presets, with a test of two instances that fire one slot once.
4. **A push to a review request starts a plan.** Needs the Forge port.
5. **Limits and the depth guard.** The caps of question 9 and the depth counter
   of question 6.
6. **The `outputs-changed` condition.** Needs the digest of the root outputs.

## Outcome

Accepted as
[ADR 267](../adr/0267-run-triggers-start-a-plan-through-the-job-service.md)
on 2026-10-20. The owner decided the kinds, the plan, the job service, the
unattended start, the runner pool, the schedule and the `follows` trigger
(decisions 1 to 8). The product manager decided the questions under the
owner's delegation of 20 October 2026 (decisions 9 to 23):

1. **"A applies"** uses only what the server can see. For the Pulumi CLI, an
   update of the kind `update` counts. For OpenTofu over the state backend,
   any final state version counts, because the request does not say which
   command wrote it. The docs page says so. A run on a workstation counts.
   The idempotency is per state version, and the limits of question 9 apply.
2. **The field `when`** has the default `applied`. The value `outputs-changed`
   comes later.
3. **The principal** is the namespace service principal, which holds only the
   action that starts a plan. For a job that a trigger starts, check 2 of
   ADR 213 decision 1 uses the current grants of the trigger's last saver:
   `unit:read` on the source, plus `unit:read-secrets` for a secret output.
   Check 1 (the output-sharing rule) still applies. The server checks at each
   read and fails closed. A saver that is missing, deleted or disabled means
   deny, and the admin flag is never rebuilt (treated as false). The
   namespace service principal gets no read action. The job carries the
   trigger ID, so that the read handler can find the saver. If that person loses
   access, the trigger needs a new save, and the console says so. The trigger
   stores its last saver, and the audit entry of each start names the trigger
   and the saver. At save, the saver must hold `unit:read` on the source.
4. **The source kinds** are `schedule`, `forge` and a new `trigger`. A manual
   fire keeps the source kind of the caller (`api`) and records the trigger ID.
   If a later decision lets a trigger apply, the scope of the schedule gate must
   also cover `trigger`.
5. **No trigger applies** in these slices.
6. **Cycles** are refused at save, with one serialized write path for each
   namespace, and a depth guard at run time.
7. **One `trigger.schedule` tick** runs each minute, and the store keeps the
   next slot of each trigger. After an outage, at most one catch-up run starts,
   for the latest missed slot. `ClaimDue` checks that a slot is a time that the
   schedule gives.
8. **A push** matches the repository and the branch. The list `paths` defaults
   to the working directory of the code location of the unit. A fork starts
   nothing. A newer push replaces the older plan.
9. **Limits** are 20 triggers for each unit and 50 followers for each source.
   An administrator can change them.
10. **No follow across namespaces.**
11. **Run order** goes to a separate RFC.
12. **No other kinds now.** Use the REST API.
