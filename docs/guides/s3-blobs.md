# S3-compatible object storage

This guide shows how to hold state content in S3-compatible storage.
The `blobtest` suite covers the adapter.
`make test-s3` runs that suite against a live S3-compatible server.

## Adapters

The object storage port is `blob.Store`.
The adapters are `blob/fs` for local disk and `blob/s3` for S3-compatible storage.

## Set the blob store

Set the store with `--blob-store` (`DUSTPAN_BLOB_STORE`).
`fs` is the default.
It holds state content in `--blob-dir` (`DUSTPAN_BLOB_DIR`).
Use `s3://<bucket>[/<prefix>]` for S3-compatible storage.

```sh
./bin/opendustpan serve --blob-store s3://dustpan-state/prod
```

```sh
DUSTPAN_BLOB_STORE=s3://dustpan-state/prod ./bin/opendustpan serve
```

An empty prefix holds objects at the top of the bucket.

## Endpoint and region

`--s3-endpoint` (`DUSTPAN_S3_ENDPOINT`) names an S3-compatible server.
Empty means AWS.
`--s3-region` (`DUSTPAN_S3_REGION`) names the signing region.
Empty means `us-east-1`.
With an endpoint, the adapter uses path-style addresses.
On AWS, it uses virtual-host addresses.
A plain `http://` endpoint logs a warning at start.
State content then travels without TLS.

```sh
./bin/opendustpan serve --blob-store s3://dustpan-state/prod --s3-endpoint https://s3.example --s3-region us-east-1
```

## Credentials

Set `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and, for temporary credentials, `AWS_SESSION_TOKEN`.
No flag takes a credential.
A missing variable stops the start with an error that names the variable.
The adapter has no cloud identity.
Pods on AWS with a role need static keys until a later release adds it.

## Start probe

At start, the adapter writes a probe object twice and deletes it.
The second write must fail with `blob.ErrExists`.
A second write that succeeds means the store ignores conditional writes.
The open then fails and the server does not start.
The error names the endpoint and the bucket.
It holds no credential.

A probe step tries again after these errors:
- a connection reset, a connection refused, an end of file or a timeout
- a DNS error that the resolver marks as temporary
- an answer with status 500, 502, 503, 504 or 429, or with the code `SlowDown`

The wait between tries grows from 100 ms to 5 s.
Each retry writes a warning to the log.
The write steps of the probe have a total time of 30 seconds.
All deletes of probe objects after a failure share one time of 5 seconds.
The worst case of the start is therefore about 35 seconds.
When the time ends, the start fails and the error names the last cause.
These times are fixed. The `serve` command has no setting for them.

All other errors fail at once, with no retry.
Examples are access denied, a missing bucket, a bad signature, a DNS name that does not exist, status 400 and status 501.
The key of the probe object is random.
When the first write finds its key in place, the object is the probe's own.
The store got an earlier try, but the answer was a 5xx answer or did not arrive.
The probe then deletes that object and uses a new key.

The error text of the store is untrusted.
The start deletes control characters from the error code of the store and cuts it to 128 bytes.

## Write-once use and the size limit

Each `Put` sends `If-None-Match: *`.
A 412 answer gives `blob.ErrExists`.
A 409 answer with code `ConditionalRequestConflict` gives `blob.ErrExists`.
A 404 answer with code `NoSuchKey` gives `blob.ErrNotFound`.
Delete of a missing key succeeds.
One `PUT` holds 5 GiB at most.
A larger object fails with an error that names the limit.

## Item key blobs and clocks

Each write of the key of a history item puts a blob under a new key, and the
record of the key names that blob. A write that does not commit within 30
minutes from the start of its transaction rolls back. The blob of a write
that rolled back stays in the store until a cleanup deletes it.

A cleanup counts a key blob that no record names as garbage only 1 hour after
the blob store wrote it. The blob store gives this time, so the rule allows a
difference of at most 15 minutes between the clock of the server and the
clock of the blob store. Keep both clocks in step, for example with NTP.

Each cleanup run measures the difference first. It writes an empty probe
object, `item-keys/clock-probe-<20 letters and digits>`, in the directory of
the namespace, reads the time that the blob store gives it, and deletes it.
If the two clocks differ by more than 15 minutes, ahead or behind, or if a key
blob has a time more than 15 minutes ahead of the server clock, the cleanup
deletes no key blob of that namespace in that run. A probe that a failed
delete left is deleted by a later run, 1 hour after it was written.

## Check the store

The store check runs a set of tests on the store of your own installation and measures it.
Use it to see how your store behaves before you rely on it.
A check adds load to the store.
On a paid store, it costs requests.
A check writes about 20 MiB in about 200 requests, and it reads, lists and deletes what it wrote.

```sh
opendustpan store check --server https://dustpan.example --target blobs
```

The target is a name, not an address.
`blobs` checks the blob store of the server.
`backup` checks the backup target of the server.
Any other value fails.
The command never takes a URL, and the server reads only its own store.
The token comes from `DUSTPAN_ADMIN_TOKEN` or from `DUSTPAN_ADMIN_TOKEN_FILE`, as for the backup command.
The command needs the action `installation:store-check` on the installation.
An administrator and the installation operator role hold it.
The command waits until the check ends and prints the report.
It exits with 0 when every case passed.
It exits with 1 when a case failed, when the check stopped before its end, or when an error stopped the command.

### What the check does

A check writes only under the prefix `store-checks/<check ID>/`.
It deletes the objects at its end.
It writes no object outside the prefix, and it reads no object outside the prefix.
The check runs these cases on the prefix:

- Put an object, then get it, and compare the bytes.
- Get a missing key and expect the not-found answer.
- Delete an object, get it again, and delete it a second time.
- Write one key twice. The store can refuse the second write or replace the object. Both are valid. The report shows which one the store does.
- Write one key from 8 parallel writers. A reader must see one whole object.
- Write and read an object of 4 MiB and compare its SHA-256 digest.
- Put with a reader that fails in the middle. The store must leave no object.
- List 3 objects and expect the key order.
- List a prefix that has no object and expect an empty list.
- Ping the store.
- Delete every object under the prefix and list the prefix again.

The check also measures these values on your store, at the time of the check:

- The latency of put, get and delete, as the 50th and the 99th percentile of 50 small objects and 4 large objects.
- The throughput of put and get, in bytes per second, with 4 objects of 4 MiB.
- The delete rate, in objects per second, with single delete requests.

The report also says:

- `conditionalWrites`: `enforced` when the store refused the second write of a key, `ignored` when it replaced the object, and `unknown` when the check stopped before this case. The server does not need the store to enforce conditional writes for correctness.
- `throttlingSeen`: `true` when a request failed with a throttling answer, for example SlowDown, after the retries of the adapter. A throttling answer that a retry got through is not counted.
- `prefixDeleted`: `false` when the store refused the delete of the prefix, or when an object stayed in the listing after the delete. A target with object lock can do this. The last case then does not pass, and the objects stay under the prefix. The server tries to delete them again for 24 hours after the check, and a lock can refuse that too. Delete the objects by hand when the lock ends.

A figure in the report is a value that one run measured. It says nothing about the store at another time, and it is not a limit or a promise of the store.
A store with object versioning can keep old versions of the objects. The check does not look at versions.

### Limits of a check

A check writes at most 1,000 objects and 256 MiB in total, with at most 64 MiB in one object.
It stops after 10 minutes.
Only one check runs at a time for each installation.
After a check ends, a new check on the same target answers 409 for 15 minutes.
A check answers 409 while the installation is in maintenance mode or while a restore runs.
A check is a durable operation, so a server process that runs with the `worker` component must run.
Without it, the start answers 503.
These figures are fixed. No flag and no setting changes them.

### Audit

The start of a check, its end and each refusal write an entry in the audit log of the installation.
The entry names the target, the principal that asked, and, at the end, the result.

### What was tested, and what should work

Keep these two apart.
This table lists the stores on which a test of this project ran the check.

| Store | Version | Date of the test | Test |
| --- | --- | --- | --- |
| Directory store of the project (`blob/fs`) | the version of this build | 2026-10-30 | The automated tests of the server run a whole check on it and compare the report. |

No S3-compatible store is in the table.
That means no test of this project ran the check on one.
A store may work with OpenDustpan when it is not in the table. The project does not say that it works until a test ran on it.
Run the check on your own store, and keep the report.
The check is not a service level, a promise of availability or a proof of compliance.
It shows how your store behaved in one run.

## Backups and moves

The backup target of the backup guide uses the same `s3://` form.
The project has no command to copy blobs between stores.
Move blobs with a backup and a restore.

## What this guide does not cover

These items are not in this release.

- GCS and Azure Blob targets.
- Presigned URLs.
- Multipart upload.
- Cloud identity.
- A bucket for each namespace.
