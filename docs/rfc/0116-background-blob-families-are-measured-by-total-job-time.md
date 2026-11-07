---
rfc: 116
title: Background blob families are measured by total job time
status: draft
created: 2026-11-01
---

# RFC 116. Background blob families are measured by total job time

Status: draft. The benchmark of the blob write design found two families that
miss the per-operation measure of ADR 281 decision 2. Both run in the
background. This RFC asks if the measure fits them, and what to use if it
does not. It decides nothing.

## Problem

ADR 281 decision 2 accepts the new write path only if, for a blob family, the
median p50 and the median p99 are at most 5 % higher, and the median
throughput is at most 5 % lower, than the old path (a PUT with
`If-None-Match`). The record is `docs/reference/blob-write-benchmark-record.md`.
The run of 2026-11-01 on MinIO, with models that follow the code, gives this:

| Family | same-az | cross-az | eu-metro |
| --- | --- | --- | --- |
| Restore copy, key absent | meets (p50 +3.5 %) | meets (+4.1 %) | misses (p50 +8.6 %, ops/s -8.0 %) |
| Audit checkpoint write | misses (p50 +57.2 %, ops/s -35.7 %) | misses (+58.3 %, -36.2 %) | misses (+30.2 %, -23.7 %) |

The hot paths (Pulumi checkpoint, journal batch, job log chunk, backup target
copy) and the unit import meet the measure at every profile. The audit
checkpoint check meets it too.

Both families that miss are background work. A person waits for neither of
them. The per-operation measure was made for the paths where a person or a CLI
waits. For a background job, the cost that counts is how long the whole job
runs, and how much other work it delays.

What causes each miss:

- **Restore copy at eu-metro.** The new path does one fence check before the
  PUT of the live key, 1 database round trip. At eu-metro a round trip is 10 ms.
  The restore runs in maintenance mode, so the installation takes no other
  writes while it runs. The restore copies up to 8 blobs at the same time.
- **Audit checkpoint write.** `storeNew` lists the copies of the scope a second
  time before it writes. That LIST bounds the copies of one sequence. With one
  LIST, the same model is within 1.7 % of the old path at every profile (p50
  -0.3 %, +1.6 %, +0.1 %). The model with one LIST is not code.

What the misses cost in whole jobs, from the same run, at 10,000 operations and
16 workers (the figures are the time of 10,000 operations at the measured
throughput, not a measure of a real job):

| Job | Profile | Old path | New path |
| --- | --- | --- | --- |
| Restore of 10,000 blobs of 256 KiB | same-az | 48.5 s | 50.3 s |
| Restore of 10,000 blobs of 256 KiB | eu-metro | 62.9 s | 68.0 s |
| 10,000 audit checkpoint writes | same-az | 28.2 s | 43.9 s |
| 10,000 audit checkpoint writes | eu-metro | 48.8 s | 64.1 s |

The cadence of the audit checkpoint write, and the count of scopes it writes
for, are not in the benchmark. A job time for a real installation needs both.

## Options

1. **Do nothing.** Keep the per-operation measure for every family. Then the
   audit checkpoint write fails ADR 281 decision 2 at every profile, and the
   restore copy fails it at eu-metro. The decision then asks for a change to
   the code, not to the measure. For the audit write, that change is to drop
   the second LIST (option 3).
2. **Measure background families by total job time.** Keep the per-operation
   measure for every path where a person or a CLI waits. For a background
   family, run the job at a stated size, for example the restore of 10,000
   blobs, and compare the total time of the job on the two paths. The record
   states the size, the profile and the limit. The limit is a ratio, an
   absolute cap, or both. This changes what "meets" means for these families
   only. It does not change the code.
3. **Drop the second LIST of the audit write, and bound the copies in another
   way.** The model with one LIST meets the per-operation measure. The second
   LIST is a safety layer: it keeps the count of copies of one sequence at most
   the count of writers that list at the same moment. Without it, more writers
   can store a copy of one sequence before any of them sees the others. The
   check reads at most 16 copies of one sequence, and more than 16 are a
   mismatch. A replacement bound needs its own design and its own test. This
   option changes security code, so it needs an argument for each layer.

Options 2 and 3 do not exclude each other. Option 2 answers "is the measure
right?". Option 3 answers "is the second LIST worth its cost?".

## Proposal

No preference on option 3: it removes a safety layer, and the numbers alone do
not justify that. The author prefers option 2 for the restore copy and, until
option 3 has a design, for the audit write too:

- A path where a person or a CLI waits keeps the per-operation measure of ADR
  281 decision 2.
- A background family is judged on the total time of one job of a stated size.
- The record states, for each background family, the job, its size, how many
  jobs run at the same time, and the limit.
- A background family that misses its limit goes back to the owner with the
  numbers, as a hot path does.

The restore is the clear case: it runs once, in maintenance mode, and adds
about 2 s to 5 s to a restore of 10,000 blobs in the benchmark. Both paths of
the benchmark ran 16 workers. The code copies 8 blobs at a time, and no
measure of that setting exists yet. The cost is a fence check that keeps a
holder that lost its claim from writing a live key.

## Open questions

- What size and what limit does each background family get? The benchmark has
  no data for the audit write cadence, the count of scopes of a large
  installation, or the size of a real restore.
- Is a ratio or an absolute cap the better limit? A ratio of 5 % on a job of 60
  s is 3 s. A cap of 15 minutes for the restore of the largest supported
  installation might say more to an operator.
- Does the audit checkpoint write share a worker with the user-facing paths? If
  it does, its extra 25 ms or so for each write delays other work, and the
  total job time is not the only cost.
- What bound on the copies of one sequence can replace the second LIST? Options
  include a bound in the database, a per-scope lock, or a fixed delay before the
  write. Each needs a test of hostile input.
- The restore model has a fence check that main did not have when the run
  started. The check is in a pending change. The run needs one more pass when
  that change merges.

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). Not applicable: this RFC changes how a benchmark is judged. It changes no write path.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
