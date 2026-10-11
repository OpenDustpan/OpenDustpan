---
rfc: 8
title: Backups and restore, built in
status: accepted
adr: 30
created: 2026-10-10
---

# RFC 8. Backups and restore, built in

Status: accepted (ADR 30) for milestone 3: backup and restore of the whole
installation, to a local directory or to S3-compatible storage. Schedules,
retention, backups of one namespace, other targets, a public format and
encryption of the whole backup stay open.

The rest stays a draft.

## Problem

OpenDustpan holds the state of a team's infrastructure. If that state is
lost, the team can no longer change its infrastructure safely. Today the
operator must back up two things that only make sense together: the database
and the blob store. A database snapshot from 02:00 and a blob store copy from
02:10 do not agree, and nothing tells the operator so.

Self-hosted Pulumi Cloud leaves backups to the operator's database and
storage tools. We want backups to be a feature of the product.

## Proposal

- **One backup is one consistent point in time.** Blobs are immutable and
  addressed by content, so a backup is a database snapshot plus every blob
  that the snapshot refers to. Blobs written after the snapshot are not
  needed. A backup is therefore consistent without stopping the server.
- **Targets are adapters.** A backup target is a port with built-in drivers:
  local disk, S3 and S3-compatible stores, Google Cloud Storage and Azure
  Blob Storage. A custom target is an SDK option, with a conformance suite
  (ADR 2).
- **Incremental by default.** A blob that is already in the target is not
  copied again. A backup lists what it holds in a manifest, which the backup
  checks after it writes.
- **Per namespace.** A backup holds all namespaces, or one. A namespace has
  one home region (ADR 4), so a backup of one namespace can stay in that
  region. The same format moves a namespace to a new server.
- **Restore is a command, and it is tested.** `opendustpan restore` loads a
  backup into an empty database and blob store. A scheduled restore test
  restores the newest backup into scratch storage and reports if it fails.
  A backup that was never restored is only a hope.
- **Schedules and retention.** The backup role (RFC 7) runs backups on a
  schedule and deletes old ones by a retention rule.

## The key-encryption key

Secrets in the database are encrypted with data keys, and a key-encryption
key outside the database encrypts the data keys (`GLOSSARY.md`). A backup
holds the encrypted data keys, never the key-encryption key.

- A backup without the key-encryption key cannot decrypt any secret. This is
  the point: a stolen backup does not leak secrets.
- A restore therefore needs the key-encryption key too. The restore command
  checks that the key opens the data keys in the backup before it writes
  anything, and stops if it does not.
- The operator keeps the key-encryption key in its own key manager. The
  product says so loudly, in the backup docs and in the backup output.

## Options

1. **Built-in backups** (above).
2. **Document how to back up the database and the blob store.** This is
   cheap, and every operator gets the consistency problem wrong once.
3. **Continuous replication instead of backups.** Replication copies
   mistakes too: a deleted stack is deleted in both places. It is not a
   backup.

## Open questions

- Is the backup format a public, versioned format, so that other tools can
  read it?
- Are backups encrypted as a whole as well, with an operator key, on top of
  the secret encryption?
- Can a user restore one unit, for example one stack from yesterday, or only
  whole namespaces?
- How does a backup work with the namespace's failover version (ADR 4) when
  the backup restores into another region?

## Outcome

Accepted in part as [ADR 30](../adr/0030-backups-and-restore.md) for
milestone 3: backup and restore of the whole installation, to a local
directory or to S3-compatible storage. Schedules, retention, backups of one
namespace, other targets, a public format and encryption of the whole backup
stay open for a later decision.
