# 281. Any S3-compatible store: the database decides which write wins

Status: accepted · 2026-10-27. Decided by the project owner on 2026-10-27. Decision 2 is final only when the benchmark condition of decision 2 holds. Supersedes ADR 31 decisions 3 and 4. Changes the ADR 30 invariant "keys are never reused" to "keys are never reused, by construction". Extends ADR 280 decision 5

## Context

ADR 31 decision 3 makes the blob port write-once. The S3 adapter sends
`If-None-Match: *` on each `Put`, and the store must refuse a second write
to a key. ADR 31 decision 4 makes `Open` write a probe object twice. If the
second write succeeds, the server does not start.

ADR 280 decision 5 says that OpenDustpan must work with any S3-compatible
store, also with a store that does not enforce conditional writes. It says
that a later decision defines how. This ADR is that decision.

Some S3-compatible stores accept `If-None-Match: *` and ignore it. Garage is
an example. With the ADR 31 rules, such a store stops the server at start.

Today the callers use `blob.ErrExists` for two different purposes:

- As a guard. A second writer of the same key gets an error, and the first
  object stays.
- As a signal. A caller that retries a write finds that its first attempt
  stored the object, and it continues.

ADR 30 decision 2 says that a backup is consistent while the server writes,
because "blobs are immutable and keys are never reused". The write-once rule
of the store is one of the reasons for this today.

The current `List` returns all the objects under a prefix in one slice. A
unit with many blobs is loaded into memory in one piece.

The blob port has no batch delete. Each delete is one request.

On 27 October 2026 the project owner answered the questions of issue #2027.
Each decision below has the label "(owner)".

## Decision

1. **Correctness never depends on a store capability (owner).** OpenDustpan
   works with any S3-compatible store. This includes a store that ignores
   `If-None-Match`. Only performance can depend on a store capability. When
   performance depends on a capability, the server reports the capability,
   and a test covers both the case with the capability and the case without
   it.
2. **The database decides which write wins (owner).** Conditional writes are
   not required.
   - Each write attempt uses a key that is unique to that attempt. The key
     holds an attempt identifier that the writer makes before the write.
   - The row that names the blob is fenced (ADR 72), and the row decides.
     The row exists already or the fenced commit writes it. A blob that no
     committed row names is garbage, and a reaper deletes it.
   - Audit checkpoints are an exception, because they must not trust the
     database. The audit log keeps every copy of a checkpoint, and a check
     reads and checks all of them. A copy that does not agree is a finding.
   - `If-None-Match: *` stays as an extra layer. The adapter still sends it.
     The store check (decision 8) reports if the store enforces it or ignores
     it.
   - **Condition.** This decision is final only when the real-store
     benchmark shows that the performance is equal. The benchmark runs the
     write path of each family in the work list below, on the same real
     store, with the old path and with the new path. The measure is this:
     for each family, the p50 and the p99 latency of the new path are not
     more than 5 percent higher than the old path, and the throughput is not
     more than 5 percent lower. Each figure is the median of 3 runs or more.
     If the benchmark does not meet the measure, the owner decides again
     before the port change merges.
3. **One delete path (owner).** A caller deletes many blobs with parallel
   single `Delete` calls.
   - The concurrency is 8. It is fixed. No flag and no setting changes it.
   - The path backs off when the store answers `SlowDown` or another
     throttling answer. The adapter reports this with `blob.ErrThrottled`.
   - The port gets no batch delete (`DeleteObjects`). A new ADR can add one
     if a measured counterexample shows that the single path does not
     converge.
   - A research calculation shows that this path converges under realistic
     load, with a concurrency of 8 or more, with the write quota of
     decision 4, and with the paged `List` of decision 6.
4. **A write quota before 1.0 (owner).** The server limits the blob bytes or
   the blob objects that a scope can write. The quota lands before 1.0. The
   default value shows in the API.
5. **A backlog gauge (owner).** A metric gives the number of pending purge
   records and the age of the oldest one. An operator sees when deletes do
   not keep up.
6. **Paged `List` (owner).** `List` returns one page at a time, ordered by
   key, with a limit for each page and a key to continue from. The server
   never loads all the blobs of a unit into memory in one piece.
7. **No `Stat` now (owner).** The port gets `Stat` only when a caller needs
   it.
8. **A minimal start-up check, and a deep store check (owner).**
   - At start, the server checks only these: the store answers, the
     credentials work, and a probe object is written, read back with its
     bytes checked, and deleted. A second write of the probe is no longer
     part of the start-up check. A store that ignores conditional writes
     starts.
   - A user-run conformance suite, `opendustpan store check`, does the deep
     checks. It runs the blob conformance cases on a throwaway prefix. It
     measures the latency, the throughput and the delete rate. It reports
     the store capabilities: conditional writes enforced or ignored, and
     throttling seen or not seen. It deletes its throwaway prefix at the
     end.
   - The check runs through an endpoint of the installation API, with an
     action and an audit entry. The CLI is a client of that endpoint. The
     request selects the store by a name from a fixed list (the live blob
     store or the backup target), never by a URL.
9. **The docs say what was tested (owner).** The docs always say exactly
   which store and which version a test ran on, and on which date. The docs
   keep this apart from what should work. This continues ADR 280
   decision 5: the docs do not say that a named third-party store works,
   unless a test ran on it.
10. **A breaking change to `blob.Store` (owner).** Before 1.0, the public
    `blob.Store` port can change in a way that breaks an adapter outside
    this repository.

### What changes in ADR 30 and ADR 31

ADR 1 forbids edits to an accepted ADR, except to mark it superseded. So
the status lines of ADR 30 and ADR 31 point to this ADR, and their text does
not change.

- ADR 31 decision 3 (write-once, enforced by the store) is superseded by
  decisions 1 and 2. The adapter still sends `If-None-Match: *` and maps the
  answers as before. A caller no longer relies on the answer.
- ADR 31 decision 4 (the open-time probe that refuses a store) is superseded
  by decision 8.
- The ADR 30 invariant "keys are never reused" stays, but it is true by
  construction: each attempt writes a new key. It no longer depends on the
  store.
- Restore is the one writer that writes an existing key again. It does so
  only when the bytes agree with the backup manifest: the size and the hash
  that the manifest records. A key that holds other bytes stops the restore
  with an error.

### The new `Put` contract

`Put` on an existing key can replace the object, or fail with
`blob.ErrExists`. A reader sees the whole old object or the whole new
object, never a part of each. A caller that needs one winner gets it from
the database, not from the store.

### The store check API

The store check follows the backup routes of the installation API.

- `POST /api/server/store-checks` starts a check and answers 202 with its
  ID. The body is `{"target": "blobs"}` or `{"target": "backup"}`. Any other
  value answers 400. Only one check runs at a time. A second request answers
  409.
- `GET /api/server/store-checks/{id}` answers the state (`running`,
  `succeeded` or `failed`) and, when done, the report: the conformance
  cases, the measured figures and the capabilities.
- The routes need the new action `server:store-check` on `@server`. An
  administrator has it.
- The start and the end of each check write an audit entry. The entry names
  the target, the principal and, at the end, the result.
- The check writes only under `store-checks/<check ID>/` and deletes that
  prefix at the end. A reaper deletes the prefix of a check that stopped.
- `opendustpan store check --target blobs` is a client of these routes.

The port is `blob.Checker` and `blob.CheckReport`.

## The work

Each item is a separate pull request, unless it says otherwise. The port and
probe change merges last, because the families below depend on create-only
until they change.

1. Pulumi checkpoints: a key for each attempt, and the fenced row decides.
2. Pulumi journal batches: the same.
3. Job log chunks: the same.
4. Audit checkpoints: keep each copy, and check all copies.
5. Unit import upload: a key for each attempt.
6. Backup target: copy each blob under its own key, and check the bytes
   against the manifest. Do not rely on `ErrExists`.
7. Restore: write an existing key again only when the bytes agree with the
   manifest.
8. Other callers that handle `blob.ErrExists` today: job plan parts,
   wrapped item keys and registry content keys. Check each one before the
   port change.
9. The port and probe change: the new `Put` contract, the minimal start-up
   check, and the changed and new `blobtest` cases. It merges last.
10. Store check: the endpoint, the action, the audit entry and the CLI
    command.
11. The reapers: delete blobs that no committed row names, with the one
    delete path.
12. Paged `List` in every adapter, and every caller moves to it.
13. The backlog gauge.
14. The write quota.
15. E2e with Garage: the suite runs on a store that ignores conditional
    writes.

## Consequences

- A store that ignores `If-None-Match` can hold OpenDustpan data.
- A failed write can leave a blob that no row names. The reapers delete it.
  Storage use can be higher for a short time.
- An adapter outside this repository must change for the new `Put`
  contract and for the paged `List`.
- The `blobtest` cases that require one winner of a parallel `Put` change.
  An adapter that replaces an object passes them if a reader never sees a
  part of each object.
- The store check gives an operator the measured figures for their own
  store. The docs give only the figures of the stores that a test ran on.
- If the benchmark of decision 2 fails, the owner decides again, and this
  ADR can change before it is accepted.
