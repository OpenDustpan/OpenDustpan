---
rfc: 32
title: Object storage adapters
status: accepted
adr: 31
created: 2026-10-10
---

# RFC 32. Object storage adapters

Status: accepted (ADR 31) for milestone 3: the S3-compatible adapter, with no
new dependency. GCS, Azure Blob, a bucket for each namespace and cloud identity
stay open.

The rest stays a draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

OpenDustpan's blob port has a local filesystem adapter only. Production
self-hosters need S3, Google Cloud Storage and Azure Blob Storage, as Scalr's
storage profiles and every hosted product provide. RFC 8 proposes object
storage drivers for backups only.

## First ideas

- Adapters for S3 (and S3-compatible stores), GCS and Azure Blob, each running
  the blobtest conformance suite.
- One driver set shared with RFC 8 backups and RFC 10 artifacts.
- Short-lived cloud credentials through workload identity where the platform
  offers it.

## Open questions

- Which dependency does each adapter need? Each SDK is a new dependency and
  needs a decision.
- Is a storage location per namespace needed (Scalr lets customers bring their
  own bucket)?

## Outcome

Accepted in part as [ADR 31](../adr/0031-s3-compatible-object-storage.md)
for milestone 3: the S3-compatible adapter, with no new dependency. GCS,
Azure Blob, a bucket for each namespace and cloud identity stay open for a
later decision.

On 2026-10-18 the owner decided that no namespace brings its own bucket
([ADR 221](../adr/0221-no-bucket-of-the-customer.md)).
The advice for data residency is to self-host. The open question "Is a
storage location per namespace needed?" is answered: no.
