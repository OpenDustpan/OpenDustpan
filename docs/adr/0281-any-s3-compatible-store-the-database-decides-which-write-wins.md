# 281. Any S3-compatible store: the database decides which write wins

Status: accepted · 2026-10-27. Decided by the project owner on 2026-10-27. Decision 2 is conditional: no pull request of work item 15 merges before the benchmark record of decision 2 exists. If the benchmark does not meet its measure, a new ADR supersedes decision 2. Supersedes ADR 31 decisions 3 and 4. Changes the ADR 30 invariant "keys are never reused" to "keys are never reused, by construction". Extends ADR 280 decision 5

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

The current `List` returns all the objects under a prefix in one slice. So
a caller loads all the blobs of a unit into memory in one piece.

The blob port has no batch delete. Each delete is one request.

On 27 October 2026 the project owner answered the questions of issue #2027.
Each decision below has the label "(owner)". A part that the author of this
ADR added has the label "(proposed by the author)". The architecture review
of the pull request of this ADR asked for the parts with that label, and
the project manager accepted them for the owner.

## Decision

1. **Correctness never depends on a store capability (owner).** OpenDustpan
   works with any S3-compatible store. This includes a store that ignores
   `If-None-Match`. Only performance can depend on a store capability.
   - (proposed by the author) When performance depends on a capability, the
     server reports the capability, and a test covers both the case with the
     capability and the case without it.
2. **The database decides which write wins (owner).** Conditional writes are
   not required.
   - Each write attempt uses a key that is unique to that attempt. The key
     holds an attempt identifier that the writer makes before the write.
   - A fenced row (ADR 72) names the blob, and the row decides. The row
     exists already or the fenced commit writes it. A blob that no committed
     row names is garbage, and a reaper deletes it with the rule of
     decision 11.
   - Audit checkpoints are an exception, because they must not trust the
     database. The audit log keeps every copy of a checkpoint, and a check
     reads and checks all of them. Decision 12 gives the conditions.
   - (proposed by the author) `If-None-Match: *` stays as an extra layer.
     The adapter still sends it. The store check (decision 8) reports if the
     store enforces it or ignores it.
   - **The benchmark condition.** This decision is final only when a
     real-store benchmark shows that the performance is equal. The
     benchmark protocol is in "The benchmark" below.
3. **One delete path (owner).** A caller deletes many blobs with parallel
   single deletes.
   - The concurrency is 8. No flag and no setting changes it.
   - The path backs off when the store answers `SlowDown` or another
     throttling answer. The adapter reports this with `blob.ErrThrottled`.
   - The port gets no batch delete (`DeleteObjects`). A new ADR can add one
     if a measured counterexample shows that the single path does not
     converge.
   - A calculation of 27 October 2026 finds that this path converges under
     the load that it assumes, with a concurrency of 8 or more, with the
     write quota of decision 4, and with the paged `List` of decision 6.
   - (proposed by the author) The one path is a function of `pkg/blob`:
     `DeleteMany(ctx context.Context, s Store, keys []Key) error`. The
     concurrency of 8 is an unexported constant in it. No caller writes its
     own delete loop. The back-off on `ErrThrottled` has these figures: a
     base of 100 milliseconds, a cap of 5 seconds, full jitter, and at most
     8 attempts for each key. After the last attempt, `DeleteMany` returns
     an error that wraps `ErrThrottled`, and the caller tries again in its
     next round. The figures go in the catalog of code rules. A test runs
     `DeleteMany` against the `blobtest` stores.
4. **A write quota before 1.0 (owner).** The server limits the blob bytes or
   the blob objects that a scope can write. The quota lands before 1.0. The
   default value shows in the API.
5. **A backlog gauge (owner).** A metric gives the number of pending purge
   records and the age of the oldest one. An operator sees when deletes do
   not keep up.
6. **Paged `List` (owner).** `List` returns one page at a time, ordered by
   key, with a limit for each page and a key to continue from. The server
   never loads all the blobs of a unit into memory in one piece.
   - (proposed by the author) The key to continue from is a `Key`, not an
     opaque token. The S3 adapter sends it as the `StartAfter` parameter of
     `ListObjectsV2`. A page holds at most 1,000 objects, the largest page
     of S3. An `After` outside the prefix fails with `ErrInvalidKey`.
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
     throttling seen or not seen.
   - The check runs through an endpoint of the installation API, with an
     action and an audit entry. The CLI is a client of that endpoint.
     "The store check" below gives the design.
9. **The docs say what was tested (owner).** The docs always say exactly
   which store and which version a test ran on, and on which date. The docs
   keep this apart from what should work. This continues ADR 280
   decision 5: the docs do not say that a named third-party store works,
   unless a test ran on it.
10. **A breaking change to `blob.Store` (owner).** Before 1.0, the public
    `blob.Store` port can change in a way that breaks an adapter outside
    this repository.
11. **The rule of the reaper (proposed by the author).** Each family writes
    the blob before it commits the row. So the reaper must not delete a
    blob of an attempt that has not committed yet.
    - Each write attempt runs with a fixed attempt deadline of 30 minutes.
      The deadline covers the blob write and the fenced commit. A commit
      after the deadline fails, and the attempt does not count.
    - The reaper deletes a blob only when no committed row names it and its
      `ModifiedAt` is older than the grace period. The grace period is
      1 hour: the attempt deadline, plus the lease of 30 seconds (ADR 280
      decision 8), plus a margin. This is the grace period that the sweep
      of Pulumi updates uses today.
    - The reaper deletes only after a read of the backups registry finds no
      running backup (ADR 280 decision 7).
    - The reaper deletes with `DeleteMany` (decision 3).
    - These are never garbage for the reaper:
      - Each audit checkpoint copy (decision 12).
      - The temporary keys of a restore while that restore runs, because
        the restore deletes them.
      - The `store-checks/` prefixes, because their own reaper deletes them
        (see "The store check").
    - An intent row that the writer commits before the `Put` is not needed,
      because the attempt deadline bounds the grace period. If a family
      cannot bound its attempt, that family gets an intent row instead.
12. **Audit checkpoint copies (proposed by the author).** The copies keep a
    trust anchor that does not depend on the database, with these
    conditions:
    - A check finds the copies only through a listing of a fixed prefix
      for each scope and position (`.../<position>/<attempt>`), never
      through a database row.
    - The check verifies the signature of each copy. These are findings: a
      position with no copy, a copy with a bad signature, and two copies
      with a valid signature that sign different hashes at one position.
    - The reaper never deletes an audit checkpoint copy.
    - The retry limit of the writer bounds the number of copies for each
      position, so a check reads a bounded set.

### What changes in ADR 30 and ADR 31

ADR 1 forbids edits to an accepted ADR, except to mark it superseded. So
the status lines of ADR 30 and ADR 31 point to this ADR, and their text does
not change.

- Decisions 1 and 2 supersede ADR 31 decision 3 (write-once, enforced by the
  store). The adapter still sends `If-None-Match: *` and maps the answers as
  before. A caller no longer relies on the answer.
- Decision 8 supersedes ADR 31 decision 4 (the open-time probe that refuses
  a store).
- The ADR 30 invariant "keys are never reused" stays, but it is true by
  construction: each attempt writes a new key. It no longer depends on the
  store.

### The new `Put` contract

`Put` on an existing key can replace the object, or fail with
`blob.ErrExists`. A reader sees the whole old object or the whole new
object, never a part of each. A failed `Put` leaves the old object. A caller
that needs one winner gets it from the database, not from the store. Every
method of the port can return `blob.ErrThrottled`.

### Restore

Restore is the one writer that writes to a key that can exist already. It
never relies on `ErrExists`, because a store that ignores conditional writes
replaces the object with no error. For each blob that the manifest names,
restore does these steps in this order:

1. `Get` the live key.
2. If the key exists, compare its size and its hash with the manifest. If
   they agree, skip the key. If they do not agree, stop the restore with
   `ErrCorrupt`.
3. If the key does not exist, check the fence again, then `Put` the bytes
   that restore checked against the manifest.

The gap between the read in step 1 and the write in step 3 is safe, because
no other writer can write the key at that time. Maintenance mode stops
every other write to the installation, and the fence of the restore
operation stops a restore holder that lost its claim. A holder whose lease
renewal fails cancels its step. These steps need no `Stat`, so decision 7
stays.

### The backup target

All the backups in one target share one copy of each blob, under the live
key. This stays. Two backups can copy the same live key at the same time.
Live keys are unique by construction, so both write the same bytes. Each
backup compares the bytes with the size and the hash that the SQL data
records, so a replace of the copy does no harm.

### The store check

- **Routes and action.** `POST /api/dustpan/v1/store-checks` starts a
  check. `GET /api/dustpan/v1/store-checks/{id}` answers its state
  (`running`, `succeeded` or `failed`) and, when done, the report. Both
  routes need the action `installation:store-check` on `@server`. Only a
  stored grant gives the action, and an administrator has it. A GET of an
  unknown ID, or of an ID that the caller cannot read, answers 404.
- **Target.** The body is `{"target": "blobs"}` or `{"target": "backup"}`.
  Any other value answers 400. The request never holds a URL.
- **A durable operation.** The check is a durable operation of ADR 72. A
  unique constraint in the database allows one running check for each
  installation. A second start answers 409. A takeover and the prefix
  reaper act on the row of the operation.
- **Budget (proposed by the author).** One check writes at most 1,000
  objects and 256 MiB in total. One object is at most 64 MiB. A check
  stops after 10 minutes. After a check ends, a new check on the same
  target answers 409 for 15 minutes. The figures go in the catalog of code
  rules and in the docs.
- **Refusals.** A check answers 409 while the installation is in maintenance
  mode or while a restore runs.
- **Audit.** The start, the end and each denial write an audit entry. The
  entry names the target, the principal and, at the end, the result.
- **Containment of the prefix.** The check writes only under
  `store-checks/<check ID>/`. Each layer below stops a wrong prefix alone:
  - The service refuses each prefix that is not exactly
    `store-checks/<check ID>/`, with a check ID that the service made. The
    refusal fails closed and writes an audit entry.
  - The service runs every case on a `blob.Scoped` view of that prefix. A
    write, a read or a delete outside it fails with `ErrOutOfScope`.
  - The prefix reaper uses the same allowlist and the same view.
  - Each layer has a test that sends a hostile prefix, for example `""`,
    `namespaces/`, `backups/` and `store-checks/../namespaces/`.
- **The report.** It holds no endpoint and no credential. A test sends an
  adapter error that holds the endpoint and checks the report. A target
  with object lock can refuse the delete of the prefix. The report then
  says so, and the delete case does not pass.
- **The service.** The check is a concrete service in
  `internal/storecheck`, not a port. Its report types live there too.
- **The docs** say that a check adds load to the store and costs requests
  on a paid store.
- `opendustpan store check --target blobs` is a client of these routes.

### The benchmark

(proposed by the author, from the architecture review) The benchmark that
decides decision 2 follows this protocol:

- The old path and the new path run on the same store, from the same client
  host, in the same time window. The runs alternate: old, new, old, new.
- The store enforces conditional writes, so that the old path runs as
  intended. The record names the store and its version (decision 9).
- Each run does a fixed number of operations for each family, at least
  10,000. The object sizes come from the recorded traffic of the `load/`
  harness.
- Throughput is operations per second and bytes per second, at a fixed
  client concurrency that the record states.
- The measure: for each family, the median p50 and the median p99 latency
  of the new path are at most 5 percent higher than the old path, and the
  median throughput is at most 5 percent lower. The record gives the spread
  of the runs with each median. There are at least 5 runs of each path.
- The measure includes the two read paths that change: the check of the
  audit copies, and the read before each write of a restore.

## The work

Each item is a separate pull request, unless it says otherwise.

- **Readers before writers.** Each family moves to a key for each attempt,
  so its row must hold the key or the attempt identifier. With more than
  one instance, an old instance must read a row that a new instance wrote.
  So each family ships in two steps: first a reader that accepts both forms
  in one tag, then a writer that uses the new key in a later tag. A row
  that a tag shipped keeps its fixed key and stays readable.
- **The attempt deadline.** The fenced commit of each family checks the
  attempt deadline of decision 11 inside the commit transaction, against
  the clock of the database, not the clock of the instance.
- **The last item waits.** Item 15 (the port and probe change) merges
  only after items 1 to 14 and the benchmark record.

1. Pulumi checkpoints: a key for each attempt, and the fenced row decides.
2. Pulumi journal batches: the same.
3. Job log chunks: the same. Today a retry reads the stored digest after
   `ErrExists`. Under the new contract, a retry can replace the chunk with
   a new seal, so the row must name the key of its own attempt.
4. Audit checkpoints: keep each copy, and check all copies (decision 12).
5. Unit import upload: a key for each attempt. A failed write deletes only
   the key of its own attempt.
6. Backup target: compare the bytes with the SQL data, and do not rely on
   `ErrExists`.
7. Restore: the order of "Restore" above.
8. Wrapped item keys. This is security code, with one test for each layer:
   - Two item key creations never give the same blob key.
   - A read compares the size and the MD5 of the blob with the record before
     it unwraps the key. A mismatch fails closed and writes an audit entry.
   - A rewrap writes a key that holds an attempt part, and the record names
     that key.
   - The reaper does not delete a new item key blob before its row commits.
9. Job plan parts: a test that two plan writes give different part keys.
10. Registry content keys: no change of behavior. The key is the hash, so a
    replace writes the same bytes.
11. Store check: the service, the durable operation, the routes, the action,
    the audit entries and the CLI command. The hostile-prefix test uses a
    real check ID of another check, not only a made-up one.
12. Paged `List` in every adapter, and every caller moves to it.
13. The reapers, with `DeleteMany` and the rule of decision 11. The race
    between a reaper delete and the start of a backup uses the guard of
    RFC 105 option 3: the read of the backups registry starts after the
    commit, and a failed read means no delete.
14. E2e with Garage: the suite runs on a store that ignores conditional
    writes.
15. The port and probe change: the new `Put` contract, the minimal start-up
    check, and the changed and new `blobtest` cases. It merges last.

Not ordered: the backlog gauge and the write quota. Each can merge at any
time, and the write quota merges before 1.0.

## Consequences

- A store that ignores `If-None-Match` can hold OpenDustpan data.
- A failed write can leave a blob that no row names. The reapers delete it
  after the grace period. Storage use can be higher for up to 1 hour.
- An adapter outside this repository must change for the new `Put`
  contract and for the paged `List`.
- The `blobtest` cases that require one winner of a parallel `Put` change.
  An adapter that replaces an object passes them if a reader never sees a
  part of each object.
- The store check gives an operator the measured figures for their own
  store. The docs give only the figures of the stores that a test ran on.
- Item 15 is one-way in practice. After it, installations can put data on
  a store that ignores conditional writes, and a return to write-once would
  strand them. This is why item 15 waits for items 1 to 14 and the
  benchmark.
- The key format for each attempt of each family stays readable for good
  after a tag ships it. The layout of the audit checkpoint copies goes in
  the reference docs when it ships, because external verifiers can depend
  on it.
