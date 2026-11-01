# 31. S3-compatible object storage

Status: accepted · 2026-10-11. Decisions 3 and 4 superseded by ADR 281

Changed by:

- ADR 35 changes decision 1: the package path of the S3 adapter.
- ADR 221 extends decision 1: no bucket for each namespace.
- ADR 281 supersedes decisions 3 and 4: write-once by the store and the
  open-time probe.
- ADR 281 changes decision 7: the blobtest case of one winner for a parallel
  put.

## Context

[RFC 32](../rfc/0032-object-storage.md) asks for object storage adapters. The
roadmap for milestone 3 takes the S3-compatible part. A brief of 2026-10-11
checked the RFC against the code. It informed this ADR.

The blob port has `Put`, `Get` and `Delete`, and a caller writes each key once
(ADR 26). The only adapter is `blob/fs`. The binary always opens it. The server
proxies every upload through the capability URLs of ADR 25 and ADR 26, so it
uses no presigned URL. ADR 30 uses a `blob.Store` as the backup target.
`go.mod` has two direct requirements, and CLAUDE.md asks for the owner's yes
before the project adds a dependency or a tool. ADR 26 says that RFC 32 needs a
prefix `List` and a check for presigned uploads.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts the S3-compatible part of RFC 32 with the scope below.

1. **Scope of milestone 3.** The package `blob/s3` is a `blob.Store` for S3 and
   for S3-compatible servers. This ADR defers these items: GCS, Azure Blob, a
   bucket for each namespace, `List`, presigned URLs, multipart upload and
   cloud identity. The need for `List` and for the presigned MD5 check in
   ADR 26 does not apply to this adapter.
2. **No new dependency.** The adapter uses `net/http`, `encoding/xml` and
   `crypto` from the standard library. It signs requests with its own code for
   AWS Signature Version 4. The project adds no Go module. The adapter reads
   `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` from the
   environment. It never reads credentials from a flag.
3. **Write-once.** `Put` sends `If-None-Match: *`. A 412 answer, and a 409
   answer with the code `ConditionalRequestConflict`, give `blob.ErrExists`. A
   404 answer with the code `NoSuchKey` gives `blob.ErrNotFound`. Delete of a
   missing key succeeds.
4. **Open-time probe.** `Open` writes a probe object twice and deletes it. If
   the second write succeeds, the store does not support conditional writes.
   `Open` then fails with an error that names the store, and the server does
   not start.
5. **Upload.** `Put` copies the reader to a temporary file, then sends the file
   with its length and the SHA-256 in the signature. A failed request restarts
   from the file. The adapter retries 5xx, 429 and `SlowDown` answers with
   backoff. An object has a limit of 5 GiB. A larger `Put` fails with an error
   that says so.
6. **Settings.** `--blob-store` (`DUSTPAN_BLOB_STORE`) is `fs` (the default,
   with `--blob-dir`) or `s3://<bucket>[/<prefix>]`. `--s3-endpoint`
   (`DUSTPAN_S3_ENDPOINT`) and `--s3-region` (`DUSTPAN_S3_REGION`) are optional
   for AWS. With an endpoint, the adapter uses path-style addresses. A plain
   `http://` endpoint starts with a warning. `cmd/opendustpan` builds the
   adapter. `dustpan.go` does not import it. The backup target of ADR 30 uses
   the same `s3://` form.
7. **Conformance.** `blob/s3` runs `blobtest.Run` against a real S3-compatible
   server, with a unique key prefix for each subtest. The suite reads the
   variables `DUSTPAN_TEST_S3_ENDPOINT`, `DUSTPAN_TEST_S3_BUCKET`,
   `DUSTPAN_TEST_S3_ACCESS_KEY` and `DUSTPAN_TEST_S3_SECRET_KEY`. It runs when
   they have values. It skips when they have none, as the PostgreSQL suite does
   for `DUSTPAN_TEST_POSTGRES_URL`. `make test-s3` runs only `./blob/s3/...`.
   `blobtest` gets three new cases that `blob/fs` also passes: a parallel `Put`
   on one key gives one success, a large object round-trips, and an early close
   of a reader does not leak. A fake server tests the error mappings, the
   retries, the probe and the signature.
8. **CI.** CI runs the S3 suite on a service container of an S3-compatible
   server. The planned image is RustFS, pinned by digest. This is a new test
   tool in CI, so it waits for the project owner's yes (CLAUDE.md, ADR 27).
   Until then, a developer runs the suite on a local server, and CI skips it. A
   container needs no GitHub Action.
9. **Moving blobs.** The project adds no command to copy blobs between stores.
   An operator moves blobs with a backup and a restore (ADR 30).

## Consequences

- Open work:
  1. New `blobtest` cases, then `blob/s3`, then the settings in
     `cmd/opendustpan`.
  2. The test variables of decision 7 and `make test-s3`. After the owner's
     yes, a CI service container with the digest of the image. The brief found
     RustFS at digest
     `sha256:1803faef57627e2d9c2e7d89d655d712ddded5389040054987163043fecb6a3c`.
     Check the digest again when the work starts.
  3. An e2e switch `E2E_BLOBS`. The OpenTofu scenario and the backup and
     restore scenario of ADR 30 run with S3 live blobs.
  4. The package comment of `blob/blob.go` lists the adapters. The install
     documents describe the settings, the credential variables and the 5 GiB
     limit.
- The adapter has no cloud identity. Pods on AWS with a role need static keys
  until a later ADR adds it. That ADR needs the owner's yes for the AWS SDK
  modules it would use.
- A store without conditional writes cannot hold OpenDustpan data. The probe
  finds this at start.
- The proof runs on a local S3-compatible server. A run on AWS S3 is manual and
  is not part of the gate.
- MinIO images were not available from the registries that the agent could
  reach on 2026-10-11. The plan uses another server image.
- GCS and Azure Blob wait for their own ADRs.
- The exit test of this part of milestone 3 is this. The `blob/s3` package
  passes `blobtest.Run`, with the three new cases, against an S3-compatible
  server. The fake-server tests pass. A server without conditional writes stops
  at start with a clear error. On Linux, on SQLite and on PostgreSQL, the
  OpenTofu e2e scenario and the backup and restore scenario of ADR 30 pass with
  the blobs on an S3-compatible server.
