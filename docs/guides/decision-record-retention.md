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

A record of `state.write` lives for the decision record retention. The default
keeps the records forever. No job deletes the records today, so the table of
decision records grows with each lock request.

## The retention setting

The installation setting `decisionRecords.retentionHours` holds the retention
period in whole hours.

| Value | Meaning |
| --- | --- |
| `0` | Keep decision records forever. This is the default. |
| `24` to `87600` | Keep decision records for this many hours (up to 10 years). |

The server refuses any other value with 400: a sign, a space, a leading zero, a
fraction, an exponent, a text, or a number below 24 or above 87600. The minimum
of 24 hours stops a typo. It is not a legal minimum.

Read the effective value and its source:

```sh
curl -H "Authorization: Bearer $TOKEN" https://dustpan.example.com/api/dustpan/v1/settings
```

The item `decisionRecords.retentionHours` has the value `0` and the source
`default` until someone changes it. Change it with a merge patch. The value is a
JSON number. Send the revision of your read in `If-Match`, and a new
`Idempotency-Key`:

```sh
curl -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -H "If-Match: $ETAG" -H "Idempotency-Key: $(uuidgen)" \
  -d '{"decisionRecords.retentionHours":8760}' \
  https://dustpan.example.com/api/dustpan/v1/settings
```

Each change writes one audit entry that names the key, the old value and the
new value. A change to a shorter period, or from `0` to a period, deletes
evidence sooner, so it needs a fresh authentication: an access token is
refused with 403 and the code `mfa.fresh-step-required`, and a console session
with a recent sign-in makes the change. A change to a longer period, or to `0`,
needs none.

The setting prunes decision records only. It never deletes or changes an audit
entry.

## Which period to choose

- PCI DSS v4.0.1 requirement 10.5.1 asks an operator in its scope to keep audit
  log history for at least 12 months, with the last 3 months available for
  immediate analysis. In that scope, set at least `8760`.
- The GDPR storage limitation principle asks an operator to keep personal data
  no longer than the purpose needs. A decision record names a principal and a
  unit. If the records hold personal data, set a period and do not keep them
  forever.

The server cannot know which rules apply to you, so the default keeps the
records and does not delete them.

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

No server route and no job calls `PruneExpired` in this version, and no code
reads `decisionRecords.retentionHours`. So the server does not delete records,
whatever the setting says, and the table still grows. A later version adds a
scheduled sweep and the lease that lets only one server instance run it. Do not
expect a prune before that version ships.

## Errors

- 503 with the code `gate.unavailable`: the decision record store cannot take
  the write during a state write. Check the health of the server with
  `opendustpan status`, then try the state write again.
