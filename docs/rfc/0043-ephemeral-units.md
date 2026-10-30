---
rfc: 43
title: Ephemeral units and time to live
status: discussing
created: 2026-10-10
---

# RFC 43. Ephemeral units and time to live

Status: discussing. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". The section "Proposal (2026-10-30)" is ready to
accept.

## Problem

Temporary environments cost money when people forget them. env zero sets a
time to live with org and project maximums and warning emails; HCP Terraform
and Pulumi Cloud destroy after inactivity or at a date; Harness has it in
limited availability.

## First ideas

- A time to live on a unit, set at creation (also by a blueprint, RFC 14) and
  editable.
- Warnings before destroy through RFC 23; the destroy is a runner job (RFC 6).
- Unlike HCP Terraform, the destroy can require approval (RFC 13).

## What is decided already

- ADR 47: a delete of a unit is a soft delete. The unit leaves every list,
  its name is free at once, and the server keeps its state, history and
  grants for the retention period. A restore brings it back.
- ADR 100: a job has the operation `destroy`. ADR 116 decision 1: a Catalog
  revision with `ensure: absent` gives a `destroy` job for a request in
  OpenDustpan, and a write-back of the file for a request in git.
- ADR 145 decision 5: one catalog of notification types.
- ADR 224 and ADR 226: gates at fixed decision points, and the facts of a
  change. A gate can ask for an approval of a destroy.
- ADR 267 decisions 14 and 18: a start that no person makes, the last saver
  of a trigger, and one durable tick each minute.
- ADR 103: freezes and break-glass. ADR 213: manual locks.
- `GLOSSARY.md` lists "TTL" under _Avoid_ for the retention period, and
  "ephemeral" alone under _Avoid_ for the never-store mark. So this RFC
  needs other words (see the glossary lines below).

## Facts from other products

Each fact has a tag: **[fetched]** means that the page was read on
2026-10-30, **[not fetched]** means a search snippet only.

- **HCP Terraform.** Two modes: a destroy at a date and time, or after a
  time with no state change. A workspace inherits the setting of its project
  and can override it. It needs workspace admin permission. Auto-destroy
  plans "do not prompt you for apply approval". After the destroy the
  workspace stays, and the field `auto-destroy-at` is unset. Notifications
  go out 12 and 24 hours before.
  <https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/deletion>
  [fetched]
- **env zero.** Admins set a default and a maximum time to live for the
  organization or a project. The limits do not apply to admins. The maximum
  counts from the creation of the environment. Emails go to the creator 2
  days, 2 hours and 30 minutes before the destroy.
  <https://docs.envzero.com/guides/policies-governance/policy-ttl> [fetched].
  A locked environment is not destroyed by its time to live.
  <https://docs.envzero.com/guides/admin-guide/environments/environment-locking>
  [not fetched]
- **Pulumi Cloud.** The time to live counts from the creation of the stack.
  An optional "Delete After Destroy" deletes the stack after a destroy that
  succeeds. <https://www.pulumi.com/docs/deployments/concepts/ttl/> [fetched]
- **Spacelift.** A scheduled delete with resource deletion runs a destroy at
  the time, and deletes the stack only after that run succeeds.
  <https://docs.spacelift.io/concepts/stack/scheduling> [fetched]
- **Port.** A timer property only marks an entity expired and sends an
  event. An automation does the destroy.
  <https://docs.port.io/build-your-software-catalog/define-your-data-model/setup-blueprint/properties/timer>
  [fetched]

No product documents what happens after a destroy that fails, except that
Spacelift keeps the stack.

## Proposal (2026-10-30)

### Decisions

1. **A destroy time on a unit.** A unit can have one **destroy time**: a
   time at which the server starts the destroy of the unit. It is a setting
   of the unit, stored in OpenDustpan, in each tool. It is not a value of
   the tool's config, so it is never in a request file in git and it has
   one home (RFC 12). A destroy time has one of two forms, or both:
   - `at`: a fixed time.
   - `inactiveFor`: a duration. The destroy time is then the time of the
     last final state version of the unit plus that duration. Each final
     state version moves it, in the transaction of the state write, through
     the tool-neutral report `job.StateVersion` (ADR 100 decision 15). This
     is the second mode of HCP Terraform and Pulumi Cloud.

   With both, the earlier time wins. The API always answers the computed
   time.
2. **Who sets it.** A person or a principal with the new action
   `unit:set-destroy-time` on the unit sets, moves or clears it, with a
   reason: `PUT` and `DELETE /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/destroy-time`.
   The built-in role that writes a unit holds the action (ADR 117 decision
   3). Each change writes an audit entry with the old and the new time.
3. **A blueprint gives a lifetime.** A blueprint document takes the optional
   field `lifetime`: `{"default": "P3D", "inactiveFor": "P2D", "max":
   "P30D"}` (each part optional), as ISO 8601
   durations of days and hours. The first state version of a unit of the
   blueprint sets its destroy time: `at` is that time plus `default`, and
   `inactiveFor` is copied. The set is
   an automatic action, so its audit entry names the blueprint version and
   the requester of the create as the setter. Schema version 1 of the
   blueprint document takes the field, because no tag shipped it (ADR 73).
4. **A lifetime limit.** A namespace, a collection and a blueprint (through
   `lifetime.max`) can set a **lifetime limit**: the longest time from the
   creation of a unit to its destroy time. The shortest limit that holds for
   a unit wins. The limit counts from the creation of the unit, as env zero
   and Pulumi Cloud do, so a person cannot extend for ever. A set or a move
   after the limit fails with `lifetime.ErrLimit`, which names the limit and
   its scope. A unit under a limit cannot clear its destroy time. Whether
   the limit also holds for administrators is owner decision D9. The action
   `namespace:manage-lifetime-limits` sets a limit on a namespace or a
   collection.

   A limit holds for every unit under it, with no exception, and never in
   silence. These rules say what it does to each unit:
   - **A new unit** under a limit receives the destroy time `at` = its
     creation plus the limit, unless a shorter one is set. The setter is
     the principal that set the limit. This holds also for a unit with no
     destroy path, and also for a unit that the CLI of a tool makes.
   - **An existing unit** with no destroy time, or with a later one: the set
     of the limit is a two-step call. `POST
     .../lifetime-limit/checks` with the new limit answers each unit that
     it changes, with its new destroy time, and stores nothing. The `PUT`
     then stores the limit and an explicit destroy time for each of those
     units, in one transaction, with one audit entry for each unit. The
     setter of each is the principal that set the limit. A unit older than
     the limit gets the destroy time now plus a grace of 72 hours, so each
     warning of decision 10 goes out first. The grace is a constant,
     `lifetime.LimitGrace`, that the API shows.
   - **A unit with no destroy path** (decision 6) also receives its destroy
     time. The server cannot run its tool, so at that time the unit goes on
     a **destroy hold** (decision 6a) until a person destroys it with the
     CLI. The answer of the check and of the `PUT` names each such unit and
     says "destroy hold at <time>".
   - **A unit that moves** into a collection with a limit follows the rule
     of an existing unit, and the move answer lists its new destroy time.
5. **No magic.** `GET .../units/{unit_id}/destroy-time` answers the time,
   the setter, the reason, the effective limit with its scope, the destroy
   path (decision 6), the warnings sent, and the state: `scheduled`,
   `waiting` (with the reason), `destroying`, `destroyed`, `failed` or
   `blocked`. The inventory list takes the filter `destroyBefore`.
6. **The destroy path.** The server picks one path:
   - A unit of a Catalog request: a revision with `ensure: absent` and the
     origin `destroy-time`. In the home `opendustpan`, a runner job destroys. In
     the home `git`, a write-back job commits `ensure: absent` with the
     delivery of the request, and the team's CI destroys.
   - A unit with runner settings (a pool and a code location): a runner job
     with the operation `destroy`, through `job.Service.Start`.
   - Any other unit, for example a unit that only a person's own CLI
     writes: no path. The server cannot run that tool. At its destroy time
     the unit goes on a destroy hold (decision 6a).
6a. **The destroy hold.** A hold is a fixed control, as a manual lock is
    (ADR 213). It stops every change except a destroy, and it acts before
    any change, never at the upload of a state:
    - **Pulumi CLI.** The server refuses the create of an update of each
      kind except `destroy` and `preview`, with 409 and the text "the
      lifetime of this stack ended at <time>: run `pulumi destroy`".
    - **OpenTofu.** The server cannot tell a destroy from an apply at the
      lock of a workspace. So it refuses each lock with 409 and the text
      "the lifetime of this workspace ended at <time>: open a destroy window,
      then run `tofu destroy`", except during a **destroy window**. A
      principal with `unit:run-destroy` opens a window with
      `POST .../units/{unit_id}/destroy-window`, a reason and a fresh
      authentication, for at most 1 hour, with an audit entry. During the
      window the lock works.
    - The server never refuses a state upload for a hold. A refused upload
      after a change of resources would lose the record of those resources.
    - When a final state version holds no resources, the hold ends, the
      unit is `destroyed`, and decision 11 applies. A window that ends with
      resources left puts the hold back, and the server sends
      `unit.destroy-overdue`.
    - While the hold lasts, the server sends `unit.destroy-overdue` once a
      day to the owners of the unit.
    - The read of the unit shows the state `hold`. The inventory list takes
      the filter `lifetime=hold`. The fact group `lifetime` (`state`,
      `destroyAt`) of `change.propose` gives the state to gates.

    The refusals are new answers of a route of the Pulumi Cloud-compatible
    API and of the TFE-compatible API. The e2e suite checks the text that
    each real CLI shows (step 9). A hold does not change the success path
    of either API.
7. **Who the destroy runs as.** As ADR 267 decision 14 does for a trigger,
   the destroy runs as the **setter**: the principal that last set the
   destroy time, or the requester of the create for a lifetime of a
   blueprint. At the start, the server checks the current grants of the
   setter: `unit:request-destroy` for a Catalog unit, else
   `unit:run-destroy`. A setter that is missing, disabled or has lost the
   action means no destroy. The state is then `blocked`, and the server
   sends `unit.destroy-blocked`. A person sets the time again to become the
   setter. The audit entry of the start names the setter and the destroy
   time.
8. **Gates and approvals apply.** The destroy is a normal change. The gates
   of `change.propose`, `change.planned` and `change.apply` apply, with the
   new value `destroy-time` of the fact `source.kind` (a minor version of the fact
   schemas of ADR 226). So a gate can ask for an approval of each automatic
   destroy, or of the destroy of a unit with a label. A destroy that waits
   for an approval past the timeout of its gate ends `expired`, and the
   server sends `unit.destroy-failed`. HCP Terraform asks for no approval.
   OpenDustpan keeps the gate model.
9. **Locks and freezes.** While a manual lock, a lock of the tool or a
   freeze holds the unit, the destroy does not start. The state is
   `waiting`, with the reason, and the server sends `unit.destroy-blocked`
   once. The destroy starts at the first tick after the lock or the freeze
   ends. A destroy never uses break-glass.
10. **Warnings.** The server sends `unit.destroy-due` 72 hours, 24 hours and
    1 hour before the destroy time, and `unit.destroy-started`,
    `unit.destroy-failed` and `unit.destroyed`. Each goes to the
    notification destinations of the namespace that subscribe to it, and to
    the owners of the unit in its context. A move of the destroy time sends
    the warnings again from the new time. These are four new types in the
    catalog of ADR 145 decision 5.
11. **After a destroy that succeeds.** A unit setting `afterDestroy` has two
    values. With `delete`, the server soft deletes the unit (ADR 47). The
    soft delete frees the name and keeps the history for the retention
    period. With `keep`, the unit stays with an empty state, and the destroy
    time is cleared, as in HCP Terraform. A Catalog request keeps its last
    revision, `absent`, with both values. The default is owner decision D8.
    The API shows the effective value and where it comes from.
12. **After a destroy that fails.** The server does not try again: a destroy
    that failed halfway needs a person. The state is `failed`, the unit
    stays, and the server sends `unit.destroy-failed` with the job. A person
    with `unit:set-destroy-time` sets a new time, or runs the destroy by
    hand.
13. **One tick.** One durable schedule, `lifetime.tick`, runs each minute,
    as ADR 267 decision 18 does. It claims each unit whose destroy time is
    past and whose state is `scheduled` or `waiting`, with the start key
    `<unit ID>/<destroy time>`. So two instances never start two destroys.
    After an outage it starts each missed destroy once.
14. **A unit for each review request.** RFC 55 (proposal of 2026-10-30,
    preview rules) makes a unit for each review request and destroys it
    when the review request closes. Its units also get a destroy time, as
    a safety net.

### Amendments that this proposal makes

- ADR 226 decision 2: the value `destroy-time` of the fact `source.kind`,
  and the fact group `lifetime` (a minor version).
- ADR 145 decision 5: the types `unit.destroy-due`, `unit.destroy-started`,
  `unit.destroy-failed`, `unit.destroyed`, `unit.destroy-blocked` and
  `unit.destroy-overdue`.
- ADR 112 decision 3: the field `lifetime` of the blueprint document.
- ADR 213 (manual locks): the destroy hold is a second fixed control of the
  same kind, with its own refusal text on the lock route of the
  TFE-compatible API and on the update create route of the Pulumi
  Cloud-compatible API.

### Owner decisions

- **D8. The default of `afterDestroy`.** In short: after the server
  destroys a stack or a workspace at its destroy time, does the empty record
  stay, or does the server delete it? Options:
  1. `keep` (recommended). HCP Terraform keeps the workspace and Pulumi
     Cloud keeps the stack by default [fetched, links above]. Nothing is
     lost, and a person can delete the empty unit later. Cost: the names
     stay taken, and empty units stay in the lists until a person deletes
     them.
  2. `delete`. Spacelift deletes the stack after a destroy that succeeds
     [fetched]. The name is free at once, and a restore still brings the
     unit back for the retention period. Cost: a team that wants to apply
     the same unit again must restore it or make it again.
- **D9. Does a lifetime limit hold for administrators?** In short: when a
  namespace says "nothing lives longer than 30 days", can an administrator
  keep one stack longer without a change of the limit? Options:
  1. Yes (recommended). An administrator who needs a longer life raises the
     limit, with its own audit entry. Every destroy time in the namespace
     then follows one rule that the API can explain. Cost: one more step for
     an administrator.
  2. No, as env zero does [fetched]. Cost: a hidden exception: two units of
     one collection can have different limits, and the read of a unit must
     say that its setter was an administrator.

### Ports

- `pkg/lifetime`: the model (`DestroyTime`, `Limit`, `State`), the errors
  (`ErrLimit`, `ErrHold`, `ErrInvalid`, `ErrNotFound`,
  `ErrConflict`), and the port `lifetime.Store`: `Set`, `Clear`, `Get`,
  `ClaimDue(now, n)`, `Change(state)`, `SetLimit`, `Limits`. Each write
  checks the fence and appends its audit entry in its transaction. The
  suite `lifetimetest` comes before any adapter. The data is namespace
  data, so a backup and a namespace backup hold it.
- `lifetime.Destroyer`, implemented in the wiring: `Path(unit)` and
  `Start(unit, setter)` over the Catalog service and `job.Service`. No
  import of a tool package.
- `internal/lifetimesvc`: the service, the tick, the warnings.

### End-to-end steps (real CLIs)

With the test setting that shortens the tick to 10 seconds (as the HA
schedules step of milestone 7 does):

1. Publish an OpenTofu blueprint and a Pulumi blueprint with
   `lifetime: {"default": "PT2M", "max": "PT10M"}` (test durations). Submit
   one request of each in the home `opendustpan`, and apply.
2. Read the destroy time of each unit, and check the setter, the limit and
   the path `catalog`.
3. Try to move the destroy time past the limit, and see `lifetime.ErrLimit`.
4. Wait for the warning `unit.destroy-due` at a webhook sink of the suite.
5. Wait for the destroy. Check that `tofu destroy` and `pulumi destroy` ran
   in the runner jobs, that the units are soft deleted, and that a restore
   brings each one back with its history.
6. Set a destroy time on a third unit, take a manual lock on it, and check
   the state `waiting` and the notification `unit.destroy-blocked`. Delete
   the lock, and check the destroy.
7. Revoke `unit:request-destroy` from the setter of a fourth unit, and
   check the state `blocked` and no job.
8. Set `inactiveFor: PT2M` on a fifth unit. Run `tofu apply` (or
   `pulumi up`) twice, one minute apart, and check that each final state
   version moves the destroy time.
9. Make one stack and one workspace that only the CLIs of the suite write,
   with no runner settings. Check a namespace limit with the check route:
   the answer names both with "destroy hold", and an older unit with a
   destroy path with the destroy time now plus 72 hours. Set the limit and
   check the explicit destroy times and one audit entry for each unit.
   After the time: `pulumi up` fails with the hold text, and `pulumi
   destroy` works. `tofu apply` fails at the lock with the hold text. Open
   a destroy window, run `tofu destroy`, and check that the hold ends.

### Security tests (ADR 250)

1. **Boundary.** An `inactiveFor` under 1 hour (test settings aside) or over
   10 years. A destroy time in the past, more than 10 years ahead, a
   duration with months or seconds, a reason over 500 characters, and a
   blueprint `lifetime` whose `default` is longer than its `max`.
2. **Contain.** The reason is text only in the notification and the audit
   entry: CR and LF removed, escaped for each chat platform.
3. **Limit.** The tick claims at most 100 units in one run. A namespace has
   at most 10,000 destroy times.
4. **Authorize at use.** A lock of a workspace on hold with no window, a
   Pulumi update of the kind `update` or `refresh` on a stack on hold, and
   a destroy window opened by a principal with no `unit:run-destroy`, or
   longer than 1 hour: each refused, each with an audit entry. At the
   start, a setter that lost the destroy action, a disabled setter, and a deleted setter: each gives `blocked`
   and no job. A test sets a destroy time with a principal of another
   namespace and sees the refusal.
5. **Fail closed.** A state write whose move of the destroy time fails
   rolls back with the state write, so a destroy time never stays behind
   an applied change. A limit set whose list of units changed between the
   check and the `PUT` fails with `lifetime.ErrConflict`. A store error at
   the tick starts nothing. An error in the read of a hold refuses the lock
   or the update create, never lets it pass. A destroy job that fails is never started
   again by the server.

### Work items

Sizes and conflict groups as in RFC 111.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| E1 | `pkg/lifetime`: model, errors, `lifetime.Store`, the suite `lifetimetest` | M | `lifetime` | none |
| E2 | SQLite and PostgreSQL adapters of `lifetime.Store`, one migration each, the backup table list | M | `store-migrations` | E1 |
| E3 | `internal/lifetimesvc`: the tick, the destroy path, the setter check, the states, the four notification types | L | `lifetime`, `notify-catalog` | E2, the job model start of a runner destroy |
| E4 | The routes, the actions `unit:set-destroy-time` and `namespace:manage-lifetime-limits`, the fact value `source.kind == "destroy-time"`, the fact group `lifetime` | M | `identity-actions`, `gate-facts` | E3 |
| E5 | The blueprint field `lifetime`, and its use at the first state version | S | `catalog-core` | M6-2, E3 |
| E6 | The e2e steps above, and the docs page "Destroy a stack or a workspace at a set time" | M | `e2e`, `docs` | E4, E5, E7, E8, E9 |
| E7 | `inactiveFor`: the move of the destroy time in the transaction of each final state version, for both tools | M | `lifetime`, `state-write` | E3 |
| E8 | The rules of a limit: the check route, and the explicit destroy times of existing units with the grace. The rule of a move. The default of `afterDestroy` (after D8 and D9) | M | `lifetime`, `store-migrations` | E4 |
| E9 | The destroy hold: the refusal of the Pulumi update create and of the OpenTofu lock, the destroy window, `unit.destroy-overdue`, the end of a hold | M | `lifetime`, `api-pulumi`, `api-tfe`, `store-migrations` | E8 |

Order: E1, E2, E3, then E4, E5 and E7 in parallel, then E8, then E9,
then E6. E2 holds the columns of `inactiveFor` and `afterDestroy` in its
one migration. E8 and E9 are in `store-migrations` in case they need a
column for the grace or the window.

### Proposed glossary lines

**Destroy time**:
The time at which the server starts the destroy of a unit (RFC 43). It is a
setting of the unit in OpenDustpan. The destroy runs as the principal that
last set it, through the gates of a destroy.
_Avoid_: TTL (that word is on the _Avoid_ list of the retention period),
expiry (alone), auto-destroy, ephemeral (alone)

**Lifetime limit**:
The longest time from the creation of a unit to its destroy time, set on a
namespace, a collection or a blueprint. The shortest limit that holds for a
unit wins.
_Avoid_: Max TTL, TTL policy

## Open questions

- Is the record deleted after the destroy, or kept (RFC 17's soft delete)?
  Answered by decision 11: a soft delete by default, `keep` on request.

## Outcome

None yet.
