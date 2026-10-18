# 115. Quotas and reservations

Status: accepted · 2026-10-17

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
     of each other rule of the blueprint. False fails the check.

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

   For a `requests` key, the usage is the count of submits of the key in
   the window. A submit counts when it passes the check, also when its job
   fails later.
5. **A reservation holds a unit for a create in flight.** A revision that
   creates a unit reserves one unit of each `units` key of its blueprint
   version, at `submit`, in one call of `Counter.Reserve`:
   - `Reserve` checks each limit, and stores the reservation only when each
     one passes, in one transaction. It returns `quota.ErrExceeded`, with
     each key, its usage and its maximum, when one fails.
   - `Reserve` is idempotent by the reservation ID, which is the revision:
     `<request ID>/<revision>`. A retry of the submit makes no second
     reservation.
   - The counter serializes the reservations of one key, for example with a
     row lock on a row of the key. So two submits at the same time never
     both take the last unit.
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
7. **A reservation expires.** Each reservation has an end time: the end
   time of the job of the revision, plus 1 day. With the job limits of
   ADR 100 decision 16 and ADR 101 decision 6, that is at most 15 days.
   After its end time, a reservation no longer counts, so a lost release
   cannot hold a slot for longer.
8. **An update or a destroy reserves nothing.** An update keeps the count. A
   destroy frees its unit when the revision ends `applied`: the counter lowers
   the settled count then, not at submit. A destroy that fails keeps the
   count.
9. **The settled count follows the inventory.** A schedule `quota.reconcile`
   counts the units of each `units` key again every hour, from the
   inventory and the Catalog records, and writes the settled count. A
   difference with the stored count gives a log entry and a metric, and the
   count of the inventory wins. So a unit that a person deletes through the
   tool API frees its slot within an hour.
10. **Recheck of quotas.** At `approval` and at `apply` (ADR 113 decision 8),
    the check runs the `expr` rules again, and checks the `units` rules with
    the reservation of the revision counted as its own. A `requests` rule
    runs at `submit` only, because it counts submits.
11. **A lower limit never deletes a unit.** A new blueprint version with a
    lower `max` than the usage refuses new creates until the usage is
    lower. It changes no unit and no reservation.
12. **The port.** `quota.Counter` has these methods:
    - `Reserve(ctx, reservation, checks)`: decision 5.
    - `Settle(ctx, id)` and `Release(ctx, id)`: decision 6. They return
      `quota.ErrNotFound` for an unknown reservation, and succeed with no
      change for a reservation that already ended in the same way.
      `quota.ErrEnded` reports a settle of a released reservation, and the
      reverse.
    - `Record(ctx, key, at, checks)`: checks the `requests` limits of the
      checks with the submit at `at` counted, and stores the submit only when
      each one passes. It returns `quota.ErrExceeded` otherwise.
    - `Usage(ctx, checks, at)`: the usage of the key of each check at `at`.
    - `SetSettled(ctx, key, count)`: decision 9.

    The writes carry the fence of the namespace (ADR 72), and the counter
    checks it in the transaction of each write. The suite
    `pkg/quota/quotatest` covers each rule of this ADR, also two
    reservations at the same time for the last unit.
13. **Usage is in the record.** The policy input (ADR 114 decision 3) holds
    the usage of each rule, so a policy can decide on it, for example "ask for
    an approval above 80 % of the quota". The revision stores the usage that
    each recheck point saw.

## Consequences

- A request in flight for 14 days holds its slot for 14 days. A team with
  many open review requests can reach its quota before any unit exists.
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
