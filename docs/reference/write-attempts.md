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
- A sweep deletes a blob that no row names only after the grace period. The
  grace period is longer than the deadline, the skew and a lease of
  30 seconds. The margin of about 27 minutes covers a difference between the
  clock of the blob store, the clock of the sweep and the clock of the
  database. So a sweep never deletes the blob of an attempt that can still
  commit.

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
