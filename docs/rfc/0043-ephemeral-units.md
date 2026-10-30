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
   one home (RFC 12).
2. **Who sets it.** A person or a principal with the new action
   `unit:set-destroy-time` on the unit sets, moves or clears it, with a
   reason: `PUT` and `DELETE /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/destroy-time`.
   The built-in role that writes a unit holds the action (ADR 117 decision
   3). Each change writes an audit entry with the old and the new time.
3. **A blueprint gives a lifetime.** A blueprint document takes the optional
   field `lifetime`: `{"default": "P3D", "max": "P30D"}`, as ISO 8601
   durations of days and hours. The first state version of a unit of the
   blueprint sets its destroy time to that time plus `default`. The set is
   an automatic action, so its audit entry names the blueprint version and
   the requester of the create as the setter. Schema version 1 of the
   blueprint document takes the field, because no tag shipped it (ADR 73).
4. **A lifetime limit.** A namespace, a collection and a blueprint (through
   `lifetime.max`) can set a **lifetime limit**: the longest time from the
   creation of a unit to its destroy time. The shortest limit that holds for
   a unit wins. The limit counts from the creation of the unit, as env zero
   and Pulumi Cloud do, so a person cannot extend for ever. A set or a move
   after the limit fails with `lifetime.ErrLimit`, which names the limit and
   its scope. A unit under a limit cannot clear its destroy time. Unlike
   env zero, the limit holds for administrators too: an administrator raises
   the limit, and that change has its own audit entry. The action
   `namespace:manage-lifetime-limits` sets a limit on a namespace or a
   collection.
5. **No magic.** `GET .../units/{unit_id}/destroy-time` answers the time,
   the setter, the reason, the effective limit with its scope, the destroy
   path (decision 6), the warnings sent, and the state: `scheduled`,
   `waiting` (with the reason), `destroying`, `destroyed`, `failed` or
   `blocked`. The inventory list takes the filter `destroyBefore`.
6. **The destroy path.** The server picks one path, and a save refuses a
   destroy time on a unit with no path, with `lifetime.ErrNoDestroyPath`:
   - A unit of a Catalog request: a revision with `ensure: absent` and the
     origin `expiry`. In the home `opendustpan`, a runner job destroys. In
     the home `git`, a write-back job commits `ensure: absent` with the
     delivery of the request, and the team's CI destroys.
   - A unit with runner settings (a pool and a code location): a runner job
     with the operation `destroy`, through `job.Service.Start`.
   - Any other unit, for example a unit that only a person's own CLI
     writes: no path. The server cannot run that tool.
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
   new value `expiry` of the fact `source.kind` (a minor version of the fact
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
11. **After a destroy that succeeds.** The server soft deletes the unit (ADR
    47), unless the unit sets `afterDestroy: keep`. The default is `delete`.
    The soft delete frees the name and keeps the history for the retention
    period. A Catalog request keeps its last revision, `absent`. With
    `keep`, the unit stays with an empty state, and the destroy time is
    cleared, as in HCP Terraform.
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
14. **Not in this proposal.** A destroy after a time with no state change
    (the second mode of HCP Terraform). A unit for each review request,
    destroyed when the review request closes: the team's CI does this
    today through the Catalog API, with the destroy time as a safety net.
    A forge event that does it is a trigger kind of RFC 24.

### Ports

- `pkg/lifetime`: the model (`DestroyTime`, `Limit`, `State`), the errors
  (`ErrLimit`, `ErrNoDestroyPath`, `ErrInvalid`, `ErrNotFound`,
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

### Security tests (ADR 250)

1. **Boundary.** A destroy time in the past, more than 10 years ahead, a
   duration with months or seconds, a reason over 500 characters, and a
   blueprint `lifetime` whose `default` is longer than its `max`.
2. **Contain.** The reason is text only in the notification and the audit
   entry: CR and LF removed, escaped for each chat platform.
3. **Limit.** The tick claims at most 100 units in one run. A namespace has
   at most 10,000 destroy times.
4. **Authorize at use.** At the start, a setter that lost the destroy
   action, a disabled setter, and a deleted setter: each gives `blocked`
   and no job. A test sets a destroy time with a principal of another
   namespace and sees the refusal.
5. **Fail closed.** A store error at the tick starts nothing. An unknown
   destroy path refuses the save. A destroy job that fails is never started
   again by the server.

### Work items

Sizes and conflict groups as in RFC 111.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| E1 | `pkg/lifetime`: model, errors, `lifetime.Store`, the suite `lifetimetest` | M | `lifetime` | none |
| E2 | SQLite and PostgreSQL adapters of `lifetime.Store`, one migration each, the backup table list | M | `store-migrations` | E1 |
| E3 | `internal/lifetimesvc`: the tick, the destroy path, the setter check, the states, the four notification types | L | `lifetime`, `notify-catalog` | E2, the job model start of a runner destroy |
| E4 | The routes, the actions `unit:set-destroy-time` and `namespace:manage-lifetime-limits`, the fact value `source.kind == "expiry"` | M | `identity-actions`, `gate-facts` | E3 |
| E5 | The blueprint field `lifetime`, and its use at the first state version | S | `catalog-core` | M6-2, E3 |
| E6 | The e2e steps above, and the docs page "Destroy a stack or a workspace at a set time" | M | `e2e`, `docs` | E4, E5 |

Order: E1, E2, E3, then E4 and E5 in parallel, then E6.

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
