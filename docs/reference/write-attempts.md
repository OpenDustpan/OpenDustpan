# Write attempts and their deadline

The server stores the content of some writes in the blob store, and the
database row that names the blob decides which write counts. Each write of
this kind is a write attempt. Each attempt writes its blob under a key of its
own, then commits the row that names the blob.

## Writes with an attempt deadline

| Write | Tool | Blob key |
| --- | --- | --- |
| Checkpoint save | Pulumi CLI | `namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>` |
| Checkpoint of a journal build | Pulumi CLI | the same key, with sequence 0 |
| Journal batch | Pulumi CLI | `namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/journal/<lowest sequence ID>-<attempt ID>` |
| Log chunk of a job | all tools | `namespaces/<namespace ID>/job/<job ID>/log/<version ID>/<chunk>-<attempt ID>` |
| Upload of a unit import | all tools | the facts file of the upload version |

## The rule

| Bound | Value |
| --- | --- |
| Attempt deadline: the time from the start of the attempt to the commit of its row | 30 minutes |
| Skew: how far the start time of an attempt can be ahead of the database clock | 2 minutes |
| Grace period: the age of a blob that no row names before a sweep deletes it | 1 hour |

- The attempt starts before the blob write, on the clock of the server
  instance.
- The commit compares the start time with the clock of the database, in the
  transaction of the commit. The clock of an instance does not decide.
- A commit after the deadline fails. The attempt does not count, and the
  server deletes its blob. A retry is a new attempt with a new start time.
- The commit checks the start time after every lock wait of its
  transaction, so a commit that a lock holds past the deadline fails too.
- A sweep takes the time from the clock of the database, not from the clock
  of the instance. When the two clocks differ by more than the skew of
  2 minutes, or when the sweep cannot read the clock of the database, the
  sweep deletes nothing in that run, and the server log tells why. A blob
  for which the blob store gives no modification time is never old.
- A sweep deletes a blob that no row names only after the grace period. The
  grace period is longer than the deadline, the skew and a lease of
  30 seconds. The margin of about 27 minutes covers a difference between the
  clock of the blob store, the clock of the sweep and the clock of the
  database. So a sweep never deletes the blob of an attempt that can still
  commit.

## The sweeps

| Blobs | Sweep | When it runs |
| --- | --- | --- |
| Checkpoints and journal batches of an update | the sweep of the update and the sweep of the stack | after a checkpoint save, and at the start of an update |
| Plans and log chunks of a job | the schedule `job.artifact-sweep` | one time each hour, on one instance |

The schedule `job.artifact-sweep` sweeps each job that has not ended, and
each job whose state changed in the last 25 hours. One run reads at most 2,000
jobs in each of the two passes (10 pages of 200). When a pass stops at this
bound, the audit entry of the run says `capped at the page limit`, and the
next run starts again from the newest jobs. Only a job that runs
writes plan and log blobs, so an ended job gets about 24 runs after its last
blob. The sweep keeps these blobs:

- each part of a plan whose row exists, also a plan that a replan cleared and
  a reserved plan with no part yet,
- the blob of each chunk that a log version counts,
- each blob younger than the grace period,
- each blob with a key that is not a plan part or a chunk attempt of the job.

Each sweep deletes nothing while a backup or a restore runs, or when it
cannot read the backups registry. It reads the registry again before each
delete. A failed read of the rows of a job deletes nothing for that job.

Each run of `job.artifact-sweep` keeps a record of what it did:

| Record | Content |
| --- | --- |
| The output of the step of its durable operation | The number of jobs, of deleted blobs, of unknown blobs and of failed jobs. The full key of each deleted blob, at most 1,000 keys. `truncated` is `true` when the run deleted more. `skipped` gives the reason when the run deleted nothing. |
| One audit entry in the scope `@server` | The action `job:artifact-sweep`, the object `job.artifact-sweep` and the counts. The reason is `unnamed job artifact older than the grace period`, or `deleted nothing:` with the reason of a run that deleted nothing. The entry names no blob key. |
| One log line, `job artifact sweep` | The same counts. |

A blob that the sweep keeps because its key is not a plan part or a chunk
attempt, or because the blob store gives no modification time for it,
counts as unknown.

## The answer of the Pulumi Cloud-compatible API

A checkpoint save or a journal batch past its deadline answers 503 with the
code `update.attempt-expired`. The Pulumi CLI sends the request again, and
the new attempt has a new start time. The server records a journal batch that it
refuses this way against its update, with the status 503.

## When you see the code

The code tells you that one request took more than 30 minutes from its start
to its commit. Examples are a long pause of the server process, a network
partition to the database, or a database that is very slow. Find the cause
in the server log and in the metrics of the database.

Keep the clocks of the server instances and of the database in sync, for
example with NTP. An instance with a clock more than 2 minutes ahead of the
database gets the same refusal for each write.
