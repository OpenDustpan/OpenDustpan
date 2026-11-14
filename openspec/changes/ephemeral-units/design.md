## Context

A unit made from a blueprint carries a time to live. The server warns once before expiry, then creates a destroy job on a runner in the same transaction as the expiry state change. A unit with an approval rule waits on an RFC 114 review request before the destroy runs. RFC 43 is a draft, so this step proves the behavior that this design gives.

## Decisions

1. **Expiry as checked value.** Store expiry as unexported expiresAt plus warned flag behind constructor NewLifetime. The constructor rejects missing time, zero time, negative duration, past time, and time past 30 days from create plus one 7 day extension, and normalizes to Unix milliseconds. Stored references use the stable ID of the unit and the stable ID of the job.
2. **Destroy as a normal job.** Run the destroy through the existing job path with the same tool, executor, pool, and gates as any other destroy, under the namespace service principal with an audit entry that names actor expiry, job ID, and reason. The destroy waits on the review request of RFC 114 and adds no second approvals route. Extend and expiry tick serialize on the unit row: expiry tick and job create commit atomically, and extend after commit is refused with 409.
3. **Warning sent once with key.** Send the warning with a per-unit idempotency key of unit ID plus expiry time. Persist the warned flag after a successful sink accept only. On crash or sink outage, retry until accept. The sink dedupes by key, so a retry never notifies twice.

## Structure

Type lifetime with unexported expiresAt and warned flag and constructor NewLifetime. Only the constructor makes a value, and it checks future time inside the maximum. Stored references use the stable ID of the unit and the stable ID of the job.

## Risks

- A destroy runs without approval on a unit that needs approval.
- A unit stays past expiry when no runner claims the destroy job.
- A warning never arrives before the destroy or arrives twice.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service checks the grant for approve and cancel on the unit object before release of the waiting destroy. | Call release without grant directly on the service; the service denies, and the test fails if the guard is removed. |
| handler | Handler checks the authorization action on the named object before the service call. | Call the review request approve path without grant directly on the handler; the handler returns 403, and the test fails if the guard is removed. |
