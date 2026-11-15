# 382. A repeatable domain refusal goes through the shared denial limiter

Status: accepted · 2026-11-15. Records a decision of the project owner of 15 November 2026, in the owner's words

Changes:

- Extends ADR 250 decision 3: the way the audit log records repeated denials. The first refusal of a key in a window writes an entry. The next entry of the key carries the count of the later refusals.

## Context

ADR 250 decision 3 (owner) says, in its layer "Fail closed and record":
"Every denial and every use of a privilege goes to the audit log."

The redeliver route of notification deliveries refuses a redeliver that comes
too soon (429) and a redeliver of a delivery at its limit (409). Each refusal
writes one denied audit entry. A script that repeats the call writes one entry
for each call, with no bound. The caller can fill the audit log of the
namespace with entries that say the same thing.

The server has a shared denial limiter for refused requests. The package
`internal/denylimit` keeps the limiter, and the package
`internal/api/refusal` writes the entries. The HTTP adapters of the tools and
the tool-neutral HTTP adapters share it. The key of a denial is the principal,
the action, the object kind and the scope. For each key, the first denial of
a 1-minute window writes an entry. The limiter counts each later denial in the
window and writes no entry for it. The count goes out with the next entry of
the key.

Two owner decisions already bound repeated refusals in this way:

- ADR 220 decision 15 (owner): "The count of refusals goes to the audit scope
  of the namespace, one entry for each minute with refusals, so the tenant sees
  them."
- ADR 253 decision 13 (owner): "The writer limits it to one record for each
  principal for each hour, with the count of the refusals since the last
  record. [...] The shared denial mechanism for refused requests replaces this
  record when it lands."

The options were these:

- A. One entry for each refusal, and a rate limit on the route.
- B. The shared denial limiter.
- C. One entry for each refusal, as now.

The project owner chose option B on 15 November 2026.

## Decision

1. **A repeatable domain refusal goes through the shared denial limiter
   (owner, 2026-11-15).** A domain refusal that the same caller can repeat,
   for example a redeliver that comes too soon or a redeliver at its limit,
   goes through the shared denial limiter. The first refusal of a key in a
   window writes the audit entry. The limiter counts the later identical
   refusals, and the next entry of the key reports the count.
2. **No refusal leaves the count (owner, 2026-11-15).** The limiter drops no
   refusal from the count. The audit log shows the first refusal and the
   count of the others, so ADR 250 decision 3 holds.
3. **New domain refusals use the limiter by default (owner, 2026-11-15).** A
   new domain refusal that a caller can repeat records its audit entry through
   the shared denial limiter.

## Consequences

- The redeliver route records its too-soon and at-limit refusals through the
  shared denial limiter. The answer to the caller does not change: 429 and 409
  as before.
- ADR 220 decision 15 and ADR 253 decision 13 stay as they are. This ADR uses
  the same rule for domain refusals: one entry with a count in place of one
  entry for each refusal.
- A domain refusal that a caller cannot repeat keeps one entry for each
  refusal.
