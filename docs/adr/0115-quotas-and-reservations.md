# 115. Quotas and reservations

Status: accepted · 2026-10-17. The `expr` rules of decision 2 and decision 13 superseded by ADRs 224 and 226. Reservations extended by ADR 241 for imports

Changed by:

- ADR 222 supersedes decision 13: usage in the policy input.
- ADR 222 changes decision 2: the Expr quota rules.
- ADR 224 supersedes decision 13: usage in the policy input, with ADR 226.
- ADR 224 changes decision 2: the Expr rules.
- ADR 226 supersedes decision 13: the quota usage in the policy input becomes
  the quota fact.
- ADR 241 extends: the reservations of an import.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.

## Context

The owner decided on 17 October 2026:

- Quotas are an SDK port with a default adapter: a counter in the database.
- Quotas of version 1 are unit counts for each blueprint, requests in a
  time window, and custom checks in Expr.
- Quota accounting counts from the inventory, plus reservations for
  requests in flight.

A request is in flight for minutes with a runner, and for days with a
review request (ADR 101 decision 6). Two submits at the same time must not
both pass the last free unit of a quota.

## Decision

1. **A tool-neutral package `pkg/quota`.** It holds the rules, the usage,
   the reservations and the port `quota.Counter`. It imports no transport
   and no store. depguard holds it to the rules of the domain packages.
2. **Three kinds of rule.** A blueprint version holds 0 to 16 rules
   (`quota.Rule`):
   - `units`: at most `max` units made from the blueprint, in a scope.
   - `requests`: at most `max` submits of revisions of the blueprint in the
     last `window`, in a scope. The window is from 1 minute to 31 days.
   - `expr`: an Expr expression that gives a boolean, with a message. It
     sees the input document of ADR 114 decision 3, with `usage`: the usage
     of each other rule of the blueprint. False fails the check. An
     expression that fails to run gives `quota.ErrRuleFailed`: the check
     cannot run, and the recheck stops with no change (ADR 113 decision 8).
     It is never a pass.

   The scope of a `units` or a `requests` rule is one of:
   - `namespace`: every request of the blueprint in the namespace,
   - `requester`: the requests of one subject.
   - `group:<name>`: the requests of the members of one group (ADR 52), at
     the time of the submit.
3. **Keys count.** Each rule and scope gives a key (`quota.Key`): the
   namespace, the blueprint, the kind and the scope value, for example
   `payments/postgres/units/requester:alice`. The counter counts each key
   apart. A rule of a later blueprint version with the same kind and scope
   counts the same key, so an upgrade does not reset a count.
4. **Usage is the settled count plus the reserved count.** For a `units` key, the usage is:
   - **settled**: the units of the key that exist, made from the blueprint
     and not deleted, as the inventory and the Catalog records give them,
     plus
   - **reserved**: the open reservations of the key.

   The Catalog records the keys of each unit: `catalog.Request.QuotaKeys`
   holds the `units` keys of the create, with the scope of the requester
   and of each group of the requester at the submit. The settled count of a
   key counts the units whose request holds the key.

   **A safe over-count.** The empty unit exists from the submit of a create
   (ADR 110 decision 6), and the settle of the reservation comes after the
   state write. So in two windows, a unit can count twice, once settled and
   once reserved: when the reconcile of decision 9 counts the unit while its
   reservation is still open, and between the state write and the settle.
   The double count can refuse a create that would fit. It can never let a
   create pass a limit. It ends at the settle or the release, or at the end
   time of the reservation.

   For a `requests` key, the usage is the count of submits of the key in
   the window. A submit counts when it passes the check, also when its job
   fails later.
5. **A reservation holds a unit for a create in flight.** A revision that
   creates a unit reserves one unit of each `units` key of its blueprint
   version, at `submit`:
   - **In the transaction of the submit.** The default counter is in the SQL
     store adapter that also holds the Catalog records. `catalog.Store.Submit`
     writes the reservation through `quota.Tx`, the counter bound to its own
     open transaction. So the revision and its reservation commit together:
     a submit that fails for any reason (a conflict, a revision in flight, a
     stale fence, a limit) keeps no reservation, and a reservation never
     exists with no revision. `quota.Counter.InTx` gives the same `Tx` to a
     caller with no transaction of its own.
   - **Reserve before record.** In that transaction, the store calls
     `Tx.Reserve` first, then `Tx.Record` for the `requests` rules. So a
     submit that a `units` limit refuses never counts in the window of a
     `requests` limit.
   - `Reserve` checks each limit, and stores the reservation only when each
     one passes. It returns `quota.ErrExceeded`, with each key, its usage
     and its maximum, when one fails.
   - `Reserve` is idempotent by the reservation ID, which is the revision:
     `<request ID>/<revision>`. A retry of the submit makes no second
     reservation.
   - The counter serializes the reservations of one key, for example with a
     row lock on a row of the key. So two submits at the same time never
     both take the last unit.
   - A revision from git that supersedes a create takes over its
     reservation, and `Tx.SetEnd` moves the end time of the reservation to
     the deadline of the new revision, in the same transaction (ADR 116
     decision 9). The create is not reserved twice.
6. **A reservation ends in one of two ways.**
   - **Settle**: the revision ends `applied`. The counter moves the unit
     from reserved to settled, in one transaction.
   - **Release**: the revision ends `rejected`, `canceled`, `failed` or
     `expired`, or a recheck fails. The counter deletes the reservation. A
     failed create that wrote a state version keeps its unit, so the
     Catalog settles it instead.

   Both are idempotent. The Catalog calls them in a durable operation of
   the kind `catalog.quota` with the start key
   `<request ID>/<revision>/quota`, so
   a crash between the end of the job and the call does not lose a slot.
7. **A reservation ends at the deadline of its revision.** Each revision
   has a deadline that the Catalog knows at the submit (ADR 116 decision
   13), and the end time of its reservation is that deadline:
   - A revision from git: the render, plus the state wait of the job model
     (14 days, `job.DefaultStateTimeout`), plus a grace of 1 day. So 15
     days.
   - A revision with a job: the submit, plus the sum of the time limits of
     the job model for a Catalog job (7 days in the queue, 7 days at one
     approval gate, 1 day for the dispatch of each of two phases, and 14
     days of state wait), plus a grace of 1 day. So 31 days.

   At the deadline, a revision with no job, or whose job ended, ends, and
   its reservation stops counting at the same time. For a revision whose
   job has not ended, the schedule `catalog.expire` moves the deadline and
   the end time of the reservation by the grace of 1 day, from
   `catalog.ExtendLead` (12 hours) before the deadline, and checks again on
   its next run (ADR 116 decision 13). The schedule runs at least once each
   hour. So the reservation of a revision whose job has not ended stays
   open with no gap, and a lost release can hold a slot for up to the grace
   (1 day) plus one schedule interval after the end of the job.
8. **An update or a destroy reserves nothing.** An update keeps the count. A
   destroy frees its unit when the revision ends `applied`: the counter lowers
   the settled count then, not at submit. A destroy that fails keeps the
   count.
9. **The settled count follows the inventory.** A schedule `quota.reconcile`
   counts the units of each `units` key again every hour, from the
   inventory and the keys that the Catalog records hold (decision 4), and
   writes the settled count. A difference with the stored count gives a log
   entry and a metric, and the count of the inventory wins. So a unit that a person deletes through the
   tool API frees its slot within an hour.
10. **Recheck of quotas.** At `approval` and at `apply` (ADR 113 decision 8),
    the check runs the `expr` rules again, and checks the `units` rules with
    the reservation of the revision counted as its own. A `requests` rule
    runs at `submit` only, because it counts submits.
11. **A lower limit never deletes a unit.** A new blueprint version with a
    lower `max` than the usage refuses new creates until the usage is
    lower. It changes no unit and no reservation.
12. **The port.** `quota.Tx` is the counter in one open transaction:
    - `Reserve(ctx, reservation, checks)`: decision 5.
    - `Record(ctx, at, checks)`: checks the `requests` limits of the checks
      with the submit at `at` counted, and stores one submit for the key of
      each check only when each one passes. It returns `quota.ErrExceeded`
      otherwise.
    - `SetEnd(ctx, id, endsAt)`: decision 5, for a supersede.

    `quota.Counter` has these methods:
    - `InTx(ctx, fence, fn)`: runs `fn` with a `Tx` in a new transaction,
      and rolls back each write of the `Tx` when `fn` returns an error.
    - `Settle(ctx, id)` and `Release(ctx, id)`: decision 6. They return
      `quota.ErrNotFound` for an unknown reservation, and succeed with no
      change for a reservation that already ended in the same way.
      `quota.ErrEnded` reports a settle of a released reservation, and the
      reverse.
    - `Usage(ctx, checks, at)`: the usage of the key of each check at `at`.
    - `SetSettled(ctx, key, count)`: decision 9.

    The writes carry the fence of the namespace (ADR 72), and the counter
    checks it in the transaction of each write. The suite
    `pkg/quota/quotatest` covers each rule of this ADR, also two
    reservations at the same time for the last unit, a transaction that
    rolls back, and the order of reserve and record. The suite
    `pkg/catalog/catalogtest` covers a failed submit that leaves no
    reservation.
13. **Usage is in the record.** The policy input (ADR 114 decision 3) holds
    the usage of each rule, so a policy can decide on it, for example "ask for
    an approval above 80 % of the quota". The revision stores the usage that
    each recheck point saw.

## Consequences

- A request in flight for 14 days holds its slot for 14 days. A team with
  many open review requests can reach its quota before any unit exists.
- The counter and the Catalog records must be in one database, so that the
  submit and the reservation share a transaction. A counter in another
  service would need another design.
- The counter needs its own tables and a row for each key. The default
  adapter is in the SQL store, in the database of the namespace.
- A `group:` scope counts the members of the group at the time of the
  submit. A person who leaves the group keeps the units that they made in
  the count of the group.
- A quota counts units, not resources or cost. A cost quota is a later
  rule kind (RFC 67).

## Open questions

- Does a namespace need quota rules of its own, across its blueprints? Version
  1 has rules in blueprints only.
- Does a `requests` rule count submits that the check refused? Version 1
  counts only the submits that passed.
- Is the `group:` scope the right one for "team", or does a team need a
  collection scope, so that the units of a collection count?
