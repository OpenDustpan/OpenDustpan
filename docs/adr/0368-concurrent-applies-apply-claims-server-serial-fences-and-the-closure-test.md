# 368. Concurrent applies: apply claims, the server serial, fences and the closure test

Status: accepted · 2026-11-14. Decided by the project owner. Accepts points 1 to 3 and 5 to 7 of RFC 129. Point 4 stays open

Changes:

- Changes ADR 26 decision 3: under a live apply claim, the server assigns the serial of a state version.
- Changes ADR 299 decision 19: a closure test replaces the rule that a replacement is never disjoint.
- Extends ADR 302 decision 6: answers the points of the RFC, except one principal for each job on the TFE-compatible path.

## Context

ADR 302 decision 6 (owner) asks for an RFC before the code of milestone 8.
Its scope is the serial under a claim, a parent pointer and a claim ID for
each state version, a fence for each claim, one principal for each job on
the TFE-compatible path, the retry interval and the timeout of the local
wrapper, and a closure test in place of the rule of ADR 299 that a
replacement is never disjoint.
[RFC 129](../rfc/0129-concurrent-applies-on-one-state.md) gives the
options and a recommendation for each point, and adds the term "apply claim".

ADR 302 decision 2 (owner) stays as it is. This ADR does not change it.

The text that this ADR changes:

- ADR 26 decision 3: "A new state version needs the lock of the caller. It
  needs the same lineage as the current state. When no state exists, any
  lineage is valid. It needs a higher serial than the current state. A force
  upload skips the lineage check and the serial check. It never skips the
  lock check." ADR 44 changes decision 3 (forced and unlocked writes need an
  action and a switch). ADR 335 extends ADR 44 decision 5.
- ADR 299 decision 19 (owner, with details by the PM): ""No conflict" holds
  only when the check proves the two closures disjoint (PM)." The accepted
  text of RFC 114 lists the cases that are never proven disjoint: "provider
  changes, replacements, renames through `moved` blocks or aliases, data
  sources, deletes, and changes of a parent resource." ADR 328 and ADR 359
  change ADR 299, but not decision 19.

On 14 November 2026 the project owner accepted RFC 129 as recommended,
except point 4. Point 4 stays open in RFC 129.

## Decision

1. **The term (owner, 2026-11-14).** An **apply claim** is the reservation
   of the resource instances that one saved plan changes and reads. A runner
   job or the local `dustpan` wrapper registers it before the apply (ADR 302
   decision 2). Code, docs and the API say "apply claim". A page that is only
   about applies can say "claim".
2. **The server assigns the serial under an apply claim (owner,
   2026-11-14).** For an upload under a live apply claim, the merged state
   version gets the serial of the current state version plus 1, and the
   server writes this serial into the stored state document. The serial of
   the client must be higher than the last client serial of the same apply
   claim, so that a replay or an older upload of the job fails. The lineage
   check stays. An upload with no apply claim keeps ADR 26 decision 3 and
   ADR 44 as they are.
3. **A parent, a base and an apply claim ID for each state version (owner,
   2026-11-14).** Each state version stores the ID of its parent: the state
   version that was current when it committed. This holds for every write,
   also for a write under the whole-unit lock. A state version under an
   apply claim also stores the ID of its base (the state version that the
   plan of the apply claim used) and the ID of the apply claim. The commit
   succeeds only when its parent is still the current state version. The
   check and the write are in one transaction with the namespace fence. The
   history shows the apply claim and the job of each merged state version.
   A reference uses the stable ID, never the serial (ADR 362 decision 1).
4. **A fence for each apply claim (owner, 2026-11-14).**
   - Each apply claim has a lease. The apply claim of a runner job lives as
     long as the durable claim of its apply phase. The apply claim of the
     local wrapper has a lease of 90 seconds, which the wrapper renews every
     30 seconds.
   - An apply claim ends at its final upload, at the end of its job, at the
     end of its lease, or by a force-release. The end is a row in the
     database.
   - Each upload checks, in its commit transaction, that its apply claim is
     live. An upload under an ended apply claim fails with 409 and the code
     `claim.ended`.
   - A force-release needs `unit:manage` and a reason. It writes an audit
     entry that names the principal.
   - An apply claim that ends with no final upload keeps its address set
     held. A new apply claim that overlaps the held set fails, and the error
     names the ended apply claim. The hold stays until a person with
     `unit:manage` clears it with a reason, or a refresh job of the unit
     ends.
5. **The retry interval and the timeout of the local wrapper (owner,
   2026-11-14).** After a refusal, the wrapper tries again with exponential
   backoff from 1 second to 30 seconds, with full jitter. It obeys
   `Retry-After`. It waits 30 minutes by default. The flag `--claim-timeout`
   takes a value from 0 (no wait) to 6 hours. Each wait prints one line with
   the blocking apply claim, its job or person, and the time waited. When the
   refusal says that the base changed, the wrapper plans again and asks for
   the confirmation again, unless the person gave `-auto-approve`.
6. **A closure test in place of "a replacement is never disjoint" (owner,
   2026-11-14).** The one `ConflictChecker` serves both the review requests
   of ADR 299 and the apply claims. A replacement of the instance X in plan A
   is disjoint from plan B only when all of these hold:
   1. X is not in the write set or the read set of B.
   2. The downstream closure of X is in the write set of A. The closure is
      each instance that reads X, directly or through other instances, by a
      reference in the plan, a `depends_on` or the `dependencies` of state.
   3. No instance of the read set or the write set of B is in that closure,
      and no instance of B reads an instance of the closure.
   4. The closure is complete. When the check cannot prove an edge, the
      answer is a conflict.

   The other cases stay never disjoint: provider changes, renames through
   `moved` blocks or aliases, data sources, deletes and changes of a parent
   resource. The approval floor of ADR 299 decision 10 does not change: a new
   replace still needs an approval.
7. **The cases that keep the whole-unit lock do not change (owner,
   2026-11-14).** This ADR adds no case to the six cases of ADR 302 decision
   2 and takes none away.

## Not decided here

- **One principal for each job on the TFE-compatible path** (RFC 129 point
  4). How the server finds the apply claim of a TFE-compatible request stays
  open. The code of an apply claim on the TFE-compatible path waits for the
  ADR that decides it.

## Consequences

- The baseline of each dialect gets the parent, base and apply claim
  columns of state versions and a table of apply claims (ADR 322).
- The conformance suite of `ConflictChecker` replaces the case
  `AReplaceIsNeverDisjoint` with closure cases, each with a fixture: a
  replacement with its closure in its own plan, a replacement whose closure
  reaches the other plan, and a closure that the check cannot prove.
- `GLOSSARY.md` gets the term "apply claim".
- The OpenSpec changes of milestone 8 cite this ADR. A change that needs the
  TFE-compatible path stays blocked on point 4 of RFC 129.
