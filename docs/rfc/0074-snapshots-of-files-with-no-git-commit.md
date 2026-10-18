---
rfc: 74
title: Snapshots of files that have no git commit
status: draft
created: 2026-10-18
---

# RFC 74. Snapshots of files that have no git commit

Status: draft. The review of the Catalog design (pull request 831) found
that ADR 110 and ADR 135 do not agree on the code of a runner job that has
no git commit. ADR 110 lists this as an open question. This RFC holds the
options. It changes no accepted ADR.

## Problem

ADR 135 gives a runner job its code only as a **source snapshot**: the raw
git pack of one commit of one repository, in the blob store. The launcher
checks the pack against the commit and writes the files. Each part of that
design starts from a commit:

- The key of a snapshot is the namespace, the forge connection, the
  repository, the commit and the hash of the source policy (decision 1).
- The server gets the pack from a forge in a `source.fetch` operation
  (decision 2).
- The launcher checks each object hash against its git object ID, and
  checks that the commit of the job is in the pack (decision 4).
- The job names the commit in `DUSTPAN_COMMIT` (decision 5).
- The server deletes the blobs 7 days after the last job. A backup does
  not hold them, because the code stays in the forge (decision 9).
- `GLOSSARY.md` says that "snapshot" is the source snapshot only.

The Catalog (ADRs 110 to 117) makes files on the server that are not a git
commit:

- **Rendered files.** A runner job of a request with the home `opendustpan`
  runs files that `catalog.Renderer` makes from a revision and its
  blueprint version: an OpenTofu root module and its `.tfvars.json` file,
  or a Pulumi YAML program and its stack settings (ADR 110 decision 8).
  ADR 110 change 3 calls this a **rendered snapshot**, names it by a
  SHA-256 in the job spec, and adds a `job.Location` of the kind `catalog`.
  These files exist nowhere else. No forge holds them.
- **One file from a forge.** A render from git reads one `request.json` at
  one commit through `catalog.RequestFiles` (ADR 110 decision 8, ADR 116
  decision 9). The server reads one file, not the tree of the commit.
- **A module at a local path.** A rendered root module can reference a
  module from a forge by a local path into a `source.fetch` snapshot (ADR
  110 change 3). So one job can need two snapshots in one work directory.

ADR 110 says that a rendered snapshot "has no git pack" and asks for an RFC
or an amendment of ADR 135 for its key, its hash, the checks of the
launcher, its retention and the local path of a module snapshot. Until an ADR
decides it, the Catalog cannot start a runner job, and the launcher has no
check for a set of files.

Other kinds can come later. RFC 54 asks about OCI artifacts and buckets as
sources. RFC 72 asks about programmatic runs through the API, which can
upload files with no commit. A decision here should not need a new design
for each of them.

## Constraints

Each option must meet these constraints. They come from accepted ADRs and
from the review.

1. **Addressed by content and immutable.** The key of a snapshot is a hash
   of its content. Two writes of the same content give the same key. No
   write changes a snapshot after it is ready. This is the rule of ADR 135
   decision 1 for git.
2. **Pinned by a digest.** The job spec names the snapshot by one digest,
   in the form `sha256:<hex>`, as an OCI reference names a manifest. The
   launcher checks the digest before it writes a file. The digest names the
   algorithm, so a later algorithm needs no new field.
3. **Reproducible.** The same revision, blueprint version and renderer
   version give the same bytes and so the same digest (ADR 110 decision 8).
   The format has no time, no user name, no host name and no order that
   depends on a map or a file system.
4. **Sized and bounded.** The server refuses a snapshot over a limit
   before it stores the blobs. The launcher refuses one over a limit before
   it writes a file. ADR 135 decision 8 gives the limits for git: 8 GiB and
   500,000 files for a checkout.
5. **Sealed when it holds secrets.** A render gives no secret value today
   (ADR 110 decision 8). Other kinds can hold one, for example a file that
   a programmatic run uploads. The server seals a snapshot with the data key
   of its namespace (ADR 135 decision 10). It seals a kind that can hold a
   secret with a key that a redaction or a purge can shred (ADR 151).
6. **Covered by backups and retention.** The rendered files exist nowhere
   else. ADR 135 decision 9 keeps a git snapshot out of a backup because
   the forge keeps the commit. That reason does not apply to rendered
   files. The retention of a snapshot must agree with the retention of the
   history items that its job made (ADR 152), and a purge of the unit must
   delete it (ADR 47, ADR 151).
7. **Tool-neutral (ADR 12).** The format and the checks live in shared
   code (`pkg/source`). No field may exist only because one tool needs it.
   The renderer adapter of each tool makes a set of files. The snapshot
   does not know which tool reads them.
8. **Fenced writes.** The server writes the record of a snapshot in a
   transaction of the store that carries the `namespace.Fence` of the namespace. Blobs
   that a write puts in the store before a failed commit go to the orphan
   cleanup (ADR 151 decision 22).
9. **Visible in history and blame (ADRs 150 to 153).** A person who reads
   the blame of a resource can follow it to the job and from the job to
   the exact files that ran. A person can compare the files of two
   revisions. A safe delete (ADR 153) and a redaction (ADR 151) reach the
   snapshot of a unit.

## Options

### Option 0. Do nothing

ADR 110 keeps change 3 as text, and no code implements it. The Catalog can
serve requests with the home `git` only. A request with the home
`opendustpan` cannot start a runner job.

- Cost: none now. The home `opendustpan` of ADR 110 decision 5 does not
  work.
- Gives: no new format to support.

### Option A. A file set: a manifest and a tarball, addressed by digest

A new kind of snapshot, **file set**, next to the git pack of ADR 135. A
file set has two blobs:

- **The manifest.** One JSON document in the canonical form of RFC 8785,
  as ADR 111 decision 6 uses for a request document. It lists each file
  with its path, its mode (regular, executable or symbolic link), its
  size, its SHA-256 and, for a link, its target. The paths are in the
  order of their bytes. The manifest also names the producer (for example
  `catalog.render`), the version of the producer, and the mounts: other
  snapshots that the launcher writes at a path below the work directory,
  each by its own digest. A module snapshot of ADR 110 change 3 is a
  mount.
- **The content.** One tar file in the POSIX format, with the files in the
  order of the manifest. Each header has a time of 0, a user and a group
  of 0, no user name and no group name. The launcher reads it with
  `archive/tar` from the standard library.

The digest of the snapshot is the SHA-256 of the manifest bytes. The
manifest holds the SHA-256 of the tar file, so the one digest pins both
blobs. The launcher checks the digest of the manifest, the hash of the tar
file, and then the hash and the size of each file as it reads it. It
applies the path rules of ADR 135 decision 4 to each path and each link.

- Cost: a second format in `pkg/source`, with its own checks and its own
  conformance tests. The console needs its own diff of two manifests
  (by path and hash) and of two files. Retention and backup need rules of
  their own, because the rules of ADR 135 decision 9 do not fit.
- Gives: every constraint, with the standard library only. The record in
  SQL can hold the manifest, which is small, so a backup, a history view
  and a diff of two revisions need no blob. The format does not depend on
  git, so a bucket source or an upload of RFC 72 can use it. The digest
  has the form of an OCI digest, so option C stays open as a later
  transport for the same manifest.

### Option B. A synthetic git commit in a repository on the server

The server keeps a bare git repository for each namespace. At the start of
a job, it writes the rendered files as a commit there, with a fixed author,
a fixed time and a message that names the revision. It then makes a
source snapshot of that commit with the path of ADR 135. The forge
connection is a connection of a new kind, `opendustpan`, that names the
repository on the server.

- Cost: the server must write git objects and keep refs, which ADR 135
  decision 2 avoids: today the server reads only the pack format. A
  repository on the server needs storage, a garbage collection of objects,
  a lock or a fence on each ref update, and a backup format of its own.
  The object format of the repository (SHA-1 or SHA-256) becomes a
  setting. A commit ID depends on the parent, so the same files after a
  different parent give a different ID. The snapshot is then reproducible
  only when the server always writes commits with no parent.
- Gives: the launcher, the checks and `DUSTPAN_COMMIT` of ADR 135 stay as
  they are. A person can clone the repository and read the history and the
  diff of the rendered files with git. A blame row links to a commit, as
  for a unit with a code location in a forge.

### Option C. An OCI artifact in the blob store

The server writes the rendered files as an OCI artifact: an OCI image
manifest with an `artifactType` for OpenDustpan, a config blob, and one
layer that is a tar file of the files. The blobs are in the blob store,
addressed by their digests as in an OCI layout. The job spec names the
manifest digest. A registry route (RFC 10) can serve the same artifact
later.

- Cost: the OCI image specification has fields for images, for example a
  platform and a media type for each layer, that a set of files does not
  need. The launcher must check the manifest, the config and each layer.
  A layer is a whole tar file, so a diff of two revisions must read both
  layers. A manifest does not list the files, so the record in SQL needs a
  file list of its own for history and for the console. A tool that pulls
  OCI artifacts expects a registry API, which OpenDustpan does not have
  today.
- Gives: a known format with a digest for each part. An OCI source of
  RFC 54 could use the same code. A team can copy an artifact with OCI
  tools when a registry route exists.

## Comparison

| Constraint | A. File set | B. Synthetic commit | C. OCI artifact |
| --- | --- | --- | --- |
| 1. Addressed by content | Yes | Yes, with no parent only | Yes |
| 2. Pinned by a digest | `sha256:` of the manifest | Commit ID, SHA-1 or SHA-256 | `sha256:` of the manifest |
| 3. Reproducible | Yes, by the format rules | Only with a fixed author, time and no parent | Yes, with a fixed config and fixed tar headers |
| 4. Bounded | Limits on the manifest | Limits of ADR 135 | Limits on each layer |
| 5. Sealed | Data key or item key | Data key or item key, and the repository on the server too | Data key or item key |
| 6. Backup and retention | Manifest in SQL, blobs by a rule | A repository to back up | File list in SQL, blobs by a rule |
| 7. Tool-neutral | Yes | Yes | Yes |
| 8. Fenced writes | One record | A record and a ref update | One record |
| 9. History and blame | Diff by manifest in the server | Diff with git | Needs a file list of its own |
| New code in the server | A manifest and a tar writer | A git object writer, refs, garbage collection | An OCI writer and a file list |
| New code in the launcher | A manifest and tar check | None | An OCI manifest and layer check |

## Proposal

Take option A, the file set.

- It meets every constraint with the standard library and no new storage
  kind on the server.
- It keeps ADR 135 as it is. A file set is a second kind of snapshot next
  to the git pack. The launcher picks the check by the kind.
- The manifest is small, so the server can keep it in SQL for the life of
  the history items that the job made. A diff, a blame view and a backup
  then read SQL, not blobs.
- Its digest has the form of an OCI digest, so a later decision can serve
  the same manifest and tar file as an OCI artifact (option C) without a
  new format.
- Option B gives git tools to the team, but it makes the server a git
  host, which ADR 135 decided against.

The proposal for each constraint:

1. **The key.** The key of a file set is its namespace and its digest. Two
   jobs that render the same bytes in one namespace share one file set.
2. **The job spec.** `job.Location` of the kind `catalog` names the digest
   (ADR 110 change 3). The launcher gets the digest from its job session
   and puts it in `DUSTPAN_SNAPSHOT`. `DUSTPAN_COMMIT` stays empty.
3. **Reproducible bytes.** `pkg/source` writes the manifest and the tar
   file. A renderer adapter gives only paths, modes and bytes. A test
   writes the same file set twice and compares the digests.
4. **Limits.** A file set has at most 64 MiB of content and 10,000 files,
   and a manifest at most 4 MiB. A rendered root module is a few KiB, so
   these limits leave room for an upload of RFC 72. The limits of ADR 135
   decision 8 still apply to the whole checkout, mounts included.
5. **Sealing.** The server seals the tar file with the data key of the
   namespace, as ADR 135 decision 10 does. A producer that can put a
   secret in a file set (none today) seals it with an item key of its
   own, so a redaction can shred it.
6. **Backup and retention.** The record of a file set, with its manifest,
   is in SQL and so in the backup. The tar file stays in the blob store as
   long as the job log of the job that ran it (ADR 152 decision 3), and is
   in the backup for that time. The end of that period deletes the tar file
   and keeps the manifest. The purge of the unit deletes both. A job after
   a restore that needs a deleted tar file renders again, and the server
   compares the new digest with the old one.
7. **Code.** The format, the checks and a conformance suite
   (`sourcetest`) live in `pkg/source`, with no import of `catalog`.
8. **Writes.** The start transaction of the job writes the record of the
   file set with the fence, after the server wrote the blobs. A blob with
   no record goes to the orphan cleanup.
9. **History.** A history item already records its job (ADR 150 decision
   3). The job names the digest. The console shows the manifest, each file
   and the diff of two manifests, under `unit:read`.

## Open questions

- **The term.** `GLOSSARY.md` says that "snapshot" is the source snapshot
  only, and ADR 110 says "rendered snapshot". Is a file set a kind of
  source snapshot, or a term of its own? The glossary changes when an ADR
  decides this.
- **One file from a forge.** Does a render from git store the
  `request.json` that it read as a file set, so that the revision can show
  the bytes after the commit leaves the forge? Or is the commit in the
  revision enough?
- **The retention period.** Is the period of a job log (ADR 152) right for
  the tar file? A team may want the files of the last applied revision of
  each unit for as long as the unit exists, also after that period.
- **A minimal `.git` directory.** ADR 135 asks if the launcher writes a
  `.git` directory with `HEAD` for tools that read git metadata. Does the
  same answer apply to a file set, which has no commit?
- **Mount paths.** Which path below the work directory does a module
  snapshot get, and can two mounts share a path prefix?
- **Renderer upgrades.** A new renderer version can give new bytes for the
  same revision. Does a rerun of an old revision use the stored file set,
  or render again and show the diff before it runs?
- **RFC 54.** Can a bucket source and an OCI source be file sets that the
  server gets, as the `source.fetch` operation gets a git pack? Then RFC 54
  needs only a fetcher for each source, not a format.

## Outcome

Empty until the project owner accepts or drops the RFC.
