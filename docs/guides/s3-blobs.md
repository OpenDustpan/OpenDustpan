# S3-compatible object storage

This guide shows how to hold state content in S3-compatible storage.
It follows ADR 31.
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
Pods on AWS with a role need static keys until a later ADR adds it.

## Start probe

At start, the adapter writes a probe object twice and deletes it.
The second write must fail with `blob.ErrExists`.
A second write that succeeds means the store ignores conditional writes.
The open then fails and the server does not start.
The error names the endpoint and the bucket.
It holds no credential.

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

If a key blob has a time more than 15 minutes ahead of the server clock, the
cleanup deletes no key blob of that namespace in that run.

## Backups and moves

The backup target of the backup guide uses the same `s3://` form.
The project has no command to copy blobs between stores.
Move blobs with a backup and a restore.

## What this guide does not cover

These items are not in milestone 3.

- GCS and Azure Blob targets.
- Presigned URLs.
- Multipart upload.
- Cloud identity.
- A bucket for each namespace.
