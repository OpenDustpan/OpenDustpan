# 199. File sets: snapshots of sources with no git commit

Status: accepted · 2026-10-18 · adds to ADR 135 · changes ADR 134, 150 and 152

Changed by:

- ADR 286 changes: the words catalog and promotion become self-service and
  failover.

## Context

[ADR 135](0135-the-source-pipeline.md) gives a runner job its code only as
a source snapshot: the git pack of one commit. The Catalog (ADR 110) makes
files on the server that have no git commit. A runner job of a request with
the home `opendustpan` runs these files. Until a decision gave them a
format, the Catalog could not start such a job.

[RFC 74](../rfc/0074-snapshots-of-files-with-no-git-commit.md) compares
four options. On 2026-10-18 the project owner accepted option A, the file
set. The owner also decided that runners are Linux only. A Linux file
system compares names byte for byte.

## Decision

1. **A file set is a second kind of snapshot.** It has a manifest and a
   tar file. It does not replace the git pack of ADR 135. The launcher
   picks its checks by the kind.
2. **The digest.** The digest of a file set is `sha256:` and the SHA-256
   of the manifest bytes. The manifest holds the SHA-256 of the tar file,
   so one digest pins both blobs. The job spec names the file set by this
   digest only.
3. **The manifest and the tar file.** The manifest is one JSON document
   in the canonical form of RFC 8785. It lists the entries, the producer,
   the tar file and the mounts. `pkg/source` writes the exact bytes of the
   manifest and of each tar header. The launcher refuses any file set that
   breaks a rule. RFC 74 section 3 gives the exact form. The same content
   gives the same bytes.
4. **Names are compared byte for byte.** Runners are Linux only. So the
   server and the launcher do not normalize Unicode and do not fold case.
   A path is valid UTF-8. Two paths that differ in bytes are two files. A
   duplicate path is an error. A path that is a proper prefix of another
   path is an error. The change adds no dependency, and `golang.org/x/text`
   stays out of `pkg/source` and of the launcher. A later decision that
   adds another operating system for runners must decide again how names
   are compared.
5. **The key.** The key of a file set is the namespace, the unit and the
   digest. Two units never share a file set. Two writes of one key give one
   record. The blobs of the write that lost go to the orphan cleanup.
6. **Mounts.** A mount names another snapshot by its reproducible key, not
   by the hash of a pack. The digest of the file set does not change when
   the forge gives a different pack for the same commit.
7. **The checks of the launcher.** The launcher checks the manifest
   against the digest, and the tar file against the manifest, before it
   writes to the work directory. It unpacks into a staging directory and
   moves the tree to the work directory in one step. A failure ends with
   exit code 68.
8. **Limits.** Each pool sets the limits for the manifest, the entries, the
   content bytes, the size of one file and the tar file. A file set over a
   limit fails the job before its first step.
9. **Sealing, backup and retention.** The server seals the tar file with
   an item key of its own, and the manifest with the data key of the
   namespace. The record with the manifest is in SQL and in the backup. A
   file set is a history item of the kind `file-set`. A redaction shreds
   the item key and deletes the tar file. It keeps the manifest.
10. **Code.** The format, the checks and the conformance suite `sourcetest`
    live in `pkg/source`. The RFC 8785 writer moves to shared code that
    `pkg/catalog` and `pkg/source` both import.

## Consequences

- The Catalog can start a runner job for the home `opendustpan`.
- ADR 134 gets the variable `DUSTPAN_SNAPSHOT` (decision 6), and decision 4
  step 4 gets a file set as well as a git snapshot.
- ADR 150 decision 1 lists the kind `file-set` as a history item. ADR 152
  decision 4 computes the expiry time of a file set from the jobs that
  name it, and the time can grow. It never gets earlier.
- The format has no case or Unicode rule. A file set that holds `A` and `a`
  is valid. A tool that runs on a file system that folds case may see one
  file. This is outside the support of the project.
- The questions that RFC 74 lists under "Open questions" are not decided
  here. Each one needs its own decision.
