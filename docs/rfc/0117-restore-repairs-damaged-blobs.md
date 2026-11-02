---
rfc: 117
title: A restore that repairs damaged live blobs
status: discussing
created: 2026-11-02
---

# RFC 117. A restore that repairs damaged live blobs

Status: discussing. An open question for the project owner. Until the owner
decides, `main` follows ADR 38 decision 6: a restore adds blob keys and
changes none.

## Problem

Issue #2237 (QA round 7) found that a restore could not continue when a live
blob was missing or damaged. Before it applies, a restore takes a backup of
the live data (ADR 38 decision 8). That backup read every live blob and
stopped at the first blob that was absent or whose bytes did not match the
size and MD5 of its live row. So the restore failed before it reached the
blob copy step, and it could not restore the case that it exists for: a lost
or damaged blob.

A blob can be damaged in these ways:

- The key is absent: a person or a tool deleted the file or the object.
- The key holds other bytes: a disk fault, a partial write, a person who
  edited the file, or the sealed bytes of another object.
- The blob store setting of the server names another store: another
  directory, bucket, prefix or credentials. Then every key reads as absent.

## What PR #2242 did

PR #2242 (merged in train 37) changed the restore so that:

1. The backup before a restore records each missing or damaged live blob in a
   `damaged` list of its manifest, and does not stop.
2. The restore writes a missing key from the backup.
3. The restore deletes a damaged live blob and writes the bytes of the backup
   in its place. Before the delete, it binds the evidence to the run (the
   digest of the manifest of the backup before the restore, stored in the
   step record), reads the live row again to confirm the damage, copies the
   damaged bytes to `backups/<backup before restore>/damaged/<key>` in the
   target, and writes an audit event.
4. Backup check and a restore refused a backup with a `damaged` list.

Architecture review round 8 found three problems (issues #2268 and #2269):

- Item 3 contradicts ADR 38 decision 6 ("Blobs are write-once, so this adds
  keys and changes none"). No RFC or ADR records that change.
- Items 1 and 2 have no bound. A wrong blob store setting makes every live
  blob read as missing. The restore then goes on and writes the blobs of the
  backup into the wrong store, where it used to stop.
- Item 4 breaks ADR 38 decision 8. The backup before a restore that found a
  missing blob could not be restored, so it could not undo the restore.

## What `main` does now

The product manager decided to make `main` conform to ADR 38 again. This is
option A below:

- The backup before a restore still records missing and damaged live blobs,
  with the reference of the live row of each, and does not stop.
- A damaged live blob stops the restore before any change of the live data.
  The text names the count, the first keys and the next step.
- The restore writes a missing key from the backup. This adds a key and
  changes none, which decision 6 allows.
- More than 10 missing live blobs, or more than 20 percent of the blobs that
  the live data names, stop the restore before any change of the live data.
  The text says to check the blob store setting. The restore answers show the
  bound as `missingBlobLimit`. This bound is a product manager choice, the
  same as the bound of other mass changes of the project (ADR 306 decision 2).
- A backup with a `damaged` list stays restorable. A restore of it leaves
  those keys as they are, and its answer names them (`notHeld`).
- The restore answers show what the backup before the restore found
  (`preBackupFound`). One audit entry holds the counts and the first keys.

## Options

### A. Stop at a damaged live blob; the operator repairs the store

The restore never writes over a live blob. The operator puts the correct
bytes back from a copy of the blob store (a volume snapshot, bucket
versioning, a copy of the files) and runs the restore again.

- Conforms to ADR 38 decision 6. No ADR change.
- The operator decides about each damaged blob. Nothing is deleted
  automatically.
- Risk: an operator with no copy of the blob store has no path in the
  product. The bytes of the backup exist in the target, but the product
  gives no command to put them at the live key. The operator can copy
  `blobs/<key>` from the target to the live key by hand, which skips every
  check.
- Risk: with the bound as it is, a small installation (fewer than 5 blobs)
  cannot restore over one missing blob: 1 of 4 is 25 percent. The operator
  must put the blob back by hand first.

### B. Replace damaged blobs behind an explicit option of each restore

A new request field, for example `repairDamaged`, false by default. Without
it, the restore does what option A does. With it, the restore does what PR
#2242 did: evidence bound to the run, a live re-check against the row, a copy
of the damaged bytes into the target, an audit event before the delete. The
restore record and its audit entry show the option. The same bound as for
missing blobs applies, and `restore --check-only` lists the damaged blobs
first, so the operator decides before the apply.

- Needs a new ADR that changes ADR 38 decisions 6 and 8, with the "Changes:"
  and "Changed by:" items.
- The operator asks for each destructive action. This follows "No magic".
- The copies under `damaged/` must be named in a manifest, so that a command
  can restore them. Without that, the undo of a repair is not complete.
- Risk: more code in a security path (delete of live data). The code of
  PR #2242 is in the history of `main` and can come back with its tests.
- Risk: an operator who sets the option without reading the list can replace
  bytes that a person changed on purpose. The live re-check against the row
  limits this to bytes that do not match their own record.

### C. Always replace damaged blobs

The behavior of PR #2242, with no option.

- Needs the same ADR change as option B.
- Fewest steps for the operator.
- Risk: a destructive action that the operator did not ask for. This
  contradicts "No magic" and fail closed.
- Risk: the same as option B, with no point where the operator can stop it.

### Do nothing

Keep option A with no further change. This is the state of `main`.

## Recommendation

Option B, if the owner wants the product to repair damaged blobs; else
option A. Option B keeps write-once as the default and makes each replacement
a request of the operator, with a record. Option C is not recommended.

## Open questions

- Does the owner accept the bound of 10 blobs or 20 percent for missing live
  blobs, which also stops a restore over one missing blob in an installation
  with fewer than 5 blobs? A "10 and 20 percent" rule would let small
  installations restore, but would not stop a wrong store setting in an
  installation with 10 blobs or fewer.
- Should the bound be a setting of the installation?
- For option B: does a repaired restore need step-up or a second person?
- For option B: what is the form of the manifest of the copies under
  `damaged/`, and which command restores them?
