---
rfc: 25
title: Drift detection and remediation
status: discussing
created: 2026-10-10
---

# RFC 25. Drift detection and remediation

Status: discussing.
[ADR 263](../adr/0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md)
decides part of detection.
[ADR 258](../adr/0258-drift-remediation-is-a-plan-that-a-person-approves.md)
decides part of remediation.
This text proposes the rest of detection, with options and a recommendation
for each open question. The ports are in `pkg/drift`. No adapter, no route and
no job code exists yet.

## Problem

Drift detection is the most common feature in the competitor review: HCP
Terraform, Pulumi Cloud, Spacelift, env zero, Scalr, Harness, Terramate, Atmos
Pro, Gruntwork, Firefly, Burrito, tofu-controller and the Pulumi Kubernetes
Operator all have it. HCP Terraform runs health assessments. Pulumi Cloud
Deployments has drift detection, but only for Pulumi programs. OpenTofu has
none. OpenDustpan has only a motivation line in RFC 6.

A team wants three things: turn drift checks on for a unit, see which units
drifted, and read what drifted.

## What the ADRs decide

| Source | Decision |
| --- | --- |
| ADR 258 decision 1 | Detection changes nothing. A drift run is a job that only plans. |
| ADR 258 decisions 2 to 4 | Remediation is a separate job. A person approves each remediation that a schedule starts. |
| ADR 263 decision 1 | A drift check is a job of the operation `refresh` with one phase, `plan`. |
| ADR 263 decision 2 | A found difference sets the status `drifted`. A clean check clears it. |
| ADR 263 decisions 3 and 4 | The server stores the addresses that differ. The context read API has a section of its own. |
| ADR 263 decision 5 | The schedule is off by default. The frequency comes from a list or a custom cron expression. |
| ADR 263 decision 6 | A change to `drifted` sends a notification. |
| ADR 145 decision 5 | A notification type is an entry of one catalog. |
| ADR 255 | The server never reads client-encrypted OpenTofu state. |
| ADR 100 decision 10 | A plan phase never holds the unit. |

## The proposed model

### One tool-neutral model

A drift check is a job. It has the operation `refresh` and one phase, `plan`.
Each tool maps it to its own command:

| Tool | Command of the plan phase | A difference shows as |
| --- | --- | --- |
| OpenTofu | `plan -refresh-only -detailed-exitcode` | exit code 2 |
| Pulumi CLI | `refresh --preview-only` | to check in the e2e step (question 6) |

The server never reads state to find a drift (question 5). The runner reduces
the plan to a drift summary. The summary holds the addresses of the resources
that differ and, for each, the kind of difference: `changed` or `deleted`. It
holds no attribute value. An unmanaged resource is not drift. A cloud resource
that no state holds is a finding of the unmanaged resource scan (ADR 163).

The tool packages stay apart (ADR 12, ADR 13). The tools share only the job,
the result and the state of a unit. A resource address is the string that the tool
uses for it.

### The drift result and the drift status

A **drift result** is what one finished check reports: the unit, the job, the
time, the addresses and a flag for a list that the runner cut at the limit. A
unit keeps its latest drift result. The **drift status** follows from it and has
three values:

| Status | Meaning |
| --- | --- |
| `drifted` | The latest check found at least one difference. |
| `in-sync` | The latest check found none. |
| `unknown` | No check ran, or a later change made the result old. |

The values follow the status words that the owner decided on 18 October 2026:
"drifted", "in sync" and "unknown".
The spelling `in-sync` follows the other wire values, such as
`awaiting-approval`. Question 3 asks whether the unit status work uses it.

The unit shows `drifted` as one badge among others. A unit can be `drifted`
and locked at the same time, because a lock is a flag (ADR 213 decision 9).
The inventory projection holds the drift status, the time and the job. The
list of states of the unit status work reads it.

The `drift` fact group of the gate model (ADR 226 decision 4) takes its
fields from the latest result: `available` is false for `unknown`, `resources` is the
count of addresses, and `ageSeconds` is the age of the check.

### The ports (`pkg/drift`)

- `Store` records the result of a check (`Record`), reads the latest result of
  a unit (`GetLatest`) and deletes it with the unit (`DeleteLatest`). `Record` returns a `Transition`, which is the
  change of the status. The SQL adapter writes the result, the projection and
  the notification in one transaction, with the fence of the namespace.
- `PolicyStore` keeps the drift schedule setting of a namespace, a collection
  or a unit (`GetPolicy`, `PutPolicy`, `DeletePolicy`). `EffectivePolicy`
  reads the three scopes together and applies the order unit, collection,
  namespace. Its method names differ from those of `Store`, so one adapter
  type can implement both.
- `Checker` starts a drift check through the job service. `Check` is for a
  person or a client, and `Scheduled` is for the schedule. The REST route and
  the schedule both call it, so every gate, freeze and lock applies.

`drifttest` is the conformance suite. It has cases for `Store` and
`PolicyStore` and a memory reference that runs them. The cases for `Checker`
need a job service. They wait for the first slice.

### Defense in depth (ADR 250)

The result comes from a runner. A runner is not trusted. So five layers each
stop a bad result alone:

1. **Boundary.** `Result.Validate` uses an allowlist: a valid unit and job,
   at most 1,000 addresses, each address a valid UTF-8 string of at most
   1,024 bytes with no control or bidirectional character, no address twice,
   and a known kind. It rejects. It does not clean.
2. **Contain.** The store writes with placeholders. The console and the
   notification show an address as text, never as markup.
3. **Limit.** The limits above, one stored result for each unit, and a drift
   check is a plan phase that has no write access to the cloud.
4. **Authorization at the point of use.** `Record` checks that the job
   belongs to the unit, is a drift check and succeeded. A runner for a
   job of another unit cannot set the status of this unit.
5. **Fail closed.** An unknown value, a stale result or a fence that does not
   match is an error. The store writes nothing. The service that calls the
   store records the refusal in the audit log.

Each layer has its own test. `pkg/drift` tests layer 1 with hostile input. The
conformance suite tests layers 3 to 5 against every adapter. Layer 2 belongs to
the SQL adapter and the console. The slices that write them add their tests.

### Fixed steps of a drift run

This section moves the allowlist of RFC 95 section D here, as ADR 260
decision 2 asks. A drift run must invoke nothing. A denylist of arguments is
not enough. `TF_CLI_ARGS`, a custom step of a team and a wrapper binary can
add an argument after any check. Three layers stop this:

1. **Fixed steps.** The server builds the steps of a drift check from the
   step planner of the tool, with a fixed argument list from an allowlist. A
   drift check takes no custom step. The spec check refuses any other
   argument.
2. **A clean environment.** The spec check refuses `TF_CLI_ARGS` and each
   `TF_CLI_ARGS_*` variable in the step environment. The launcher refuses
   them at start.
3. **A plan check.** The server reads the plan with the strict parser (RFC 95
   section E). The check fails when the plan holds an invocation of an
   action. This layer does not depend on the arguments or on the environment.
   ADR 258 gave a scheduled refresh a plan phase, so this layer now exists
   for a drift check.

The runner image, and any wrapper binary in it, are outside the reach of the
server. The team owns the image. The docs page must say so in plain words. For
the Pulumi CLI, `refresh` runs no hook and no `command` create or update, as
we read the docs. A component hook under `refresh --run-program` is not ruled
out. The e2e step of question 6 must prove both.

### The schedule

A **drift schedule** is the setting that turns on the checks of a unit. It has
two fields: `enabled` and a frequency. The frequency is a preset or a custom
cron expression, and never both. A unit can have a setting. A collection and a
namespace can have a default. The most specific setting wins: unit, then
collection, then namespace. Where no setting exists, no check starts on its
own (ADR 263 decision 5).

A check that a schedule starts has the source kind `schedule` (ADR 226
decision 4). It starts through `job.Service`, so every gate, freeze and lock
applies to it as to any other job.

### The notification

A change of the status to `drifted` sends the notification type
`unit.drifted` with severity `warning`. A change from `drifted` to `in-sync`
sends `unit.drift-cleared` with severity `info`. A failed check is already
`job.failed` (ADR 145 decision 5). The dedupe key is the unit ID and the job
ID, so a repeated write sends nothing twice. The summary holds the unit
reference and the count. It holds no address list, because a notification
holds names and counts only (ADR 145 decision 4). The link goes to the page of
the unit.

### The context section

The section has the name `drift`. Its action is `unit:read` on the unit, as for
`health`. Its answer holds the status, the time, the job ID and the first 100
addresses with a `truncated` flag. A reader treats the addresses as data.

## Open questions

Each question has options and a recommendation. The recommendation is the
proposal of this text. The owner decides.

1. **How a job says that it is a drift check.** A manual `refresh`, a
   remediation `refresh` (plan then write) and a drift check (plan only) all
   have the operation `refresh`.
   - (a) A field `purpose` on the job spec, with the value `drift-check`. The
     spec fixes it at start (ADR 100 decision 5). It selects the phase list.
   - (b) A fifth operation `drift-check`. This breaks "four operations" in ADR
     100 decision 3.
   - (c) Infer it from `source.kind` `schedule`. A manual check then has no
     way to exist.

   *Recommendation: (a).* `Store.Record` needs the purpose to check the job.
2. **The scope of the built-in gate.** ADR 258 decision 3 says that the gate
   covers each job with `source.kind` `schedule` whose operation is `apply`,
   `destroy` or `refresh`. A scheduled drift check has that operation and
   that source. It has no apply phase, so no approval can help. If the gate
   fires at `change.planned` for it, each scheduled check would wait for an
   approval that does nothing.
   - (a) Say that the scope is each job of those operations *that has an
     apply phase*.
   - (b) Exempt `purpose` `drift-check` by name.

   *Recommendation: (a).* A test must show that a scheduled drift check
   never enters `awaiting-approval`. This needs a note on ADR 258.
3. **The status values and their wire spelling.** The three values above
   follow the owner's words. The unit status work (RFC 26) decides the
   `states` array and the wire values.
   - (a) Keep `drifted`, `in-sync` and `unknown` and ask that work to use
     them.
   - (b) Wait for that work and change the constants.

   *Recommendation: (a).* The word "in sync" also names a unit-wide badge
   that needs more than the drift status (no apply needed, no failure). The
   drift status only feeds it.
4. **What a failed or canceled check does.**
   - (a) Nothing. The unit keeps its last result and its time. The failure
     is a `job.failed` notification.
   - (b) Set `unknown` at once.
   - (c) Keep the result, and set `unknown` when it is older than three
     periods.

   *Recommendation: (a) now, (c) with the schedule.* Also, a state write by
   another job (apply, destroy, remediation) should set `unknown`, because it
   changes what the last check saw. That needs the state version writes of
   the job model.
5. **Where the changed addresses come from.** RFC 25 asked whether showing
   them needs reading state (RFC 39).
   - (a) The runner reduces the plan to a summary, and the server stores it.
   - (b) The server parses the stored plan with the strict parser.
   - (c) The server reads the state and compares.

   *Recommendation: (a), with (b) as a check later.* No state read is
   needed. ADR 255 keeps client-encrypted OpenTofu state unread by the server,
   and a runner job already uploads the `plan-display` part of its plan, which
   the runner makes with the key of the team (ADR 255 decision 8). So
   client-side encryption does not block a summary from the runner. The spike
   must prove that a unit with client-side encryption still gives the
   addresses.
6. **The Pulumi CLI command and its exit code.** `refresh --preview-only`
   must show a difference in a form that a runner step can read without
   parsing text. The docs do not promise one.
   - (a) Run an e2e step on the latest CLI first (CLAUDE.md asks for the e2e
     step before the endpoint), then pick the form.
   - (b) Ship the OpenTofu path alone first.

   *Recommendation: (b) for the first slice, (a) for the second.* The slices
   below follow this order.
7. **The schedule and the trigger object.** Run triggers (RFC 24) have a
   `schedule` trigger kind for each unit.
   - (a) The drift schedule is its own setting. It uses the same durable
     schedule machinery.
   - (b) A drift schedule is a trigger of kind `schedule` with the purpose
     `drift-check`.

   *Recommendation: (a).* Drift needs inheritance from a collection and a
   namespace, and a trigger object does not have it.
8. **The frequency list and the limits.**
   - Presets: `hourly`, `every-6-hours`, `daily` and `weekly`.
   - A custom cron expression has five fields, runs in UTC and has a minimum
     interval of one hour.
   - Spread: the first check of a unit is at an offset from a hash of the unit
     ID inside the period. This is Scalr's idea (RFC 25 first ideas).

   *Recommendation: as listed.* The cron parser needs a dependency decision.
   The repository has no cron parser today. The schedule slice must ask
   before it adds one (CLAUDE.md).
9. **Pause after repeated failures.**
   - (a) Pause the schedule of a unit after five failed checks in a row. Show
     `paused` on the setting. A successful manual check resumes it.
   - (b) Never pause. Alert only.

   *Recommendation: (a).* A broken credential should not run every hour for
   ever.
10. **Who may set the schedule and start a check.**
    - (a) Starting a check needs `unit:run-plan`. Writing a setting needs the
      action that manages settings of that object.
    - (b) New actions `unit:check-drift` and `unit:manage-drift`.

    *Recommendation: (a).* A drift check changes nothing, as a plan does.
    This adds no action. Each route still declares its action (ADR 17).
11. **Ignore a drift (ADR 258 decision 5).** The action, the audit entry and
    the expiry of an ignore stay open. *Recommendation:* decide them in the
    remediation theme. No slice below needs them.
12. **The result list in the context section.**
    - (a) 100 addresses and a `truncated` flag.
    - (b) Counts only, and a link to the job.

    *Recommendation: (a).* Keep attribute names for a later change. An
    attribute name can leak a design, and the value never goes in.
13. **The lock of the state during the plan.** A refresh-only plan writes
    nothing, so it needs no lock. A lock held by an apply would fail the check.
    *Recommendation:* the fixed arguments for OpenTofu include `-lock=false`.
    A manual lock or a freeze holds a drift check as it holds any job. The
    spike proves the argument list.

## Slices after this design

Each slice is one thin end-to-end change. Each carries its e2e step first and
its docs page (ADR 214). The follow-up issues carry the dependencies.

1. **Check drift on demand, OpenTofu.** The `purpose` field, the fixed steps,
   the drift summary of the runner, `Store` over SQL, a route to start a
   check and one to read the latest result. Needs the job service and routes, and the
   claimer.
2. **Check drift on demand, Pulumi CLI.** The same with `refresh
   --preview-only` (question 6).
3. **The `drifted` status in the inventory and the console.** The projection
   field and the badge. Needs the unit status work.
4. **The drift section of the context read API.**
5. **The schedule.** `PolicyStore` over SQL, the routes of the setting, the
   spread, the pause. Needs run triggers or the durable schedule.
6. **The notification.** The two types and the emitting transaction. Needs the
   outbox and the dispatcher.

## Outcome

Accepted in part as
[ADR 258](../adr/0258-drift-remediation-is-a-plan-that-a-person-approves.md)
on 2026-10-20: no automatic apply by default. A remediation is a job whose plan
a person approves.

Accepted in part as
[ADR 263](../adr/0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md)
on 2026-10-20: a drift check is a plan-only refresh job, the unit shows
`drifted`, the context read API has a drift section, and the schedule is off
by default. The questions above stay open.
