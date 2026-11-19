# Decision record retention

This guide tells you how the server keeps and deletes the decision records of
state writes.

## What the server records

Each state write is one evaluation of the decision point `state.write`. This
includes each lock request of the OpenTofu CLI on a workspace that is not
locked. The server writes one decision record for each evaluation, also when no
gate and no access limit exists. A user with the right grant reads the records
through the record read route. See
[Gates and access limits on state writes](state-write-gates.md).

A lock request on a workspace that is already locked asks no gate and writes no
record. The server answers 409. So `-lock-timeout` retries on a held lock add
no record.

## Why the records grow

A record of `state.write` lives for the audit retention. No job deletes the
records today, so the table of decision records grows with each lock request.

## The prune method

The Go port `gate.Records` has the method `PruneExpired`. An embedder that uses
the SDK calls it with a request from `gate.NewPruneRequest`. The request holds:

- A cutoff time. It must be set.
- A batch size from 1 to 1000.

The method deletes, in one transaction, at most the batch size of records of the
namespace that have these properties:

- The decision point is `state.write`.
- The time of the record is before the cutoff.

It deletes the records with the lowest IDs first, and returns the IDs that it
deleted. The caller gives an audit entry, and the method writes it in the same
transaction, also when no record matches. A record of another decision point, a record at or after the cutoff and
a record of another namespace stay. If the call fails, it deletes nothing.

The method takes the cutoff from its caller. It does not read the audit
retention setting, and it holds no timer and no lease.

## What is not available yet

No server route and no job calls `PruneExpired` in this version. So the server
does not delete records, and the table still grows. A later version adds a
scheduled sweep and the lease that lets only one server instance run it. Do not
expect a prune before that version ships.

## Errors

- 503 with the code `gate.unavailable`: the decision record store cannot take
  the write during a state write. Check the health of the server with
  `opendustpan health --listen <address>`, then try the state write again.
