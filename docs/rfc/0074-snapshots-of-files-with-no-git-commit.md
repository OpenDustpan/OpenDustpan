---
rfc: 74
title: Snapshots of files that have no git commit
status: draft
created: 2026-10-18
---

# RFC 74. Snapshots of files that have no git commit

Status: draft. A review of the Catalog design against the source pipeline
found that ADR 110 and ADR 135 do not agree on the code of a runner job
that has no git commit. ADR 110 lists this as an open question. This RFC holds the
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
   transaction of the store that carries the `namespace.Fence` of the
   namespace. Blobs that a write puts in the store before a failed commit
   go to the orphan cleanup (ADR 151 decision 22).
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
  as ADR 111 decision 6 uses for a request document. It lists each entry
  with its path, its mode, its size, its SHA-256 and, for a link, its
  target. It also names the producer (for example `catalog.render`), the
  version of the producer, the SHA-256 of the tar file, and the mounts:
  other snapshots that the launcher writes at a path below the work
  directory. A module snapshot of ADR 110 change 3 is a mount.
- **The content.** One tar file with the entries in the order of the
  manifest. The launcher reads it with `archive/tar` from the standard
  library.

The digest of the file set is the SHA-256 of the manifest bytes. The
manifest holds the SHA-256 of the tar file, so the one digest pins both
blobs. The exact form and the order of the checks are in the proposal.

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

ADR 135 decision 2 says that the `source.fetch` operation never writes a
work tree. It does not forbid a git repository on the server. So option B
does not contradict ADR 135, but it adds a kind of storage that ADR 135
does not have.

- Cost: the server must write git objects and keep refs. Today it only
  reads the pack format. A repository on the server needs storage, a
  garbage collection of objects, a lock or a fence on each ref update, and
  a backup format of its own. The object format of the repository (SHA-1
  or SHA-256) becomes a setting. A commit ID depends on the parent, so the
  same files after a different parent give a different ID. The snapshot is
  then reproducible only when the server always writes commits with no
  parent. A commit message that names the revision also changes the ID:
  the same files from two revisions give two commits. So the message must
  be fixed too, and the link from the commit to the revision moves to SQL.
  For a person to clone the repository, the server needs a git smart HTTP
  route, with its own authorization and limits.
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

**Variant C2: one layer for each file.** The ORAS convention stores each
file as one layer, with its path in the annotation
`org.opencontainers.image.title`, and the empty config
(`application/vnd.oci.empty.v1+json`).

- Cost: the paths are in annotations, which the OCI specification does
  not restrict, so the path rules of this RFC apply on top. A symbolic
  link and an executable mode have no standard field, so they need
  annotations of our own, which other OCI tools ignore. A unit with 10,000
  files gives a manifest with 10,000 layers, more than many registries
  accept (Docker builds stop at 127 layers). Each file is a blob, so the
  blob store holds many small objects.
- Gives: the manifest lists each file by path and digest, so a diff of two
  revisions reads the two manifests only, as in option A. ORAS can pull
  the artifact when a registry route exists.

## Comparison

| Constraint | A. File set | B. Synthetic commit | C. OCI artifact |
| --- | --- | --- | --- |
| 1. Addressed by content | Yes | Yes, with no parent and a fixed message only | Yes |
| 2. Pinned by a digest | `sha256:` of the manifest | Commit ID, SHA-1 or SHA-256 | `sha256:` of the manifest |
| 3. Reproducible | Yes, by the format rules | Only with a fixed author, time, message and no parent | Yes, with a fixed config and fixed tar headers |
| 4. Bounded | Limits on the manifest | Limits of ADR 135 | Limits on each layer |
| 5. Sealed | Data key or item key, manifest and tar | Data key or item key, and the repository on the server too | Data key or item key |
| 6. Backup and retention | Manifest in SQL, tar by a rule | A repository to back up | File list in SQL (C) or manifest (C2), blobs by a rule |
| 7. Tool-neutral | Yes | Yes | Yes |
| 8. Fenced writes | One record | A record and a ref update | One record |
| 9. History and blame | Diff by manifest in the server | Diff with git | Needs a file list of its own (C), manifest diff (C2) |
| New code in the server | A manifest and a tar writer | A git object writer, refs, garbage collection, a smart HTTP route | An OCI writer and a file list |
| New code in the launcher | A manifest and tar check | None | An OCI manifest and layer check |

## Proposal

We propose option A, the file set.

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
- Option B gives git tools to the team. It does not contradict ADR 135,
  but it adds a git repository, refs, garbage collection and a smart HTTP
  route to the server, and its commit IDs are reproducible only with a
  fixed parent, author, time and message.

The proposal for each constraint follows.

### 1. The key

We propose the key namespace, unit and digest. The unit is the tool, the
namespace and the name of the unit (ADR 13). Two jobs of one unit that
render the same bytes share one file set. Two units never share a file
set, also when their digests are equal. So a purge, a redaction and the
end of a retention period act on the file sets of one unit only, and
never on the files of another unit.

Rejected alternative: the key namespace and digest, with a reference
count of the units that use each file set. It stores equal content once.
But a purge or a redaction of one unit must then count the other units,
and must keep the blobs while one other unit uses them. Rendered root
modules are a few KiB, so the storage that a shared key saves is small.

### 2. The job spec

`job.Location` of the kind `catalog` names the digest (ADR 110 change 3).
The launcher gets the digest from its job session and puts it in
`DUSTPAN_SNAPSHOT`. `DUSTPAN_COMMIT` stays empty (see the open questions).

### 3. The exact canonical form

`pkg/source` writes the manifest and the tar file. A renderer adapter
gives only paths, modes, link targets and bytes. The writer refuses an
input that breaks a rule below. The launcher refuses a file set that
breaks a rule below. A test writes the same file set twice and compares
the digests.

**Paths.**

- The separator is `/`. A path has no leading `/` and no trailing `/`.
- No part of a path is empty, `.` or `..`.
- A path is valid UTF-8 in Unicode Normalization Form C (NFC). A path that
  is not in NFC is an error. The writer does not normalize it.
- Two paths that are equal after NFC or after Unicode case folding are an
  error, because a file system that folds case or normalizes names writes
  them to one file.
- A duplicate path is an error.

**Directories.** The manifest and the tar file have no directory entries.
A directory exists only as a parent of a file or a link, so a file set has
no empty directory. The launcher makes each parent directory with mode
`0755`.

**Symbolic links.**

- A target is relative. An absolute target is an error.
- A target is in normal form: no empty part, no `.` part, and `..` only
  at the start.
- The target, resolved from the directory of the link, is a regular file
  or a directory inside the file set. A target that leaves the file set or
  names nothing in it is an error.
- A target is never a link, and no part of the resolved path is a link.
  So no chain goes through a link.
- No path of the file set is below the path of a link. For example, the
  link `m` and the file `m/x` are an error.

**Tar.**

- The format is PAX (POSIX.1-2001). Each header has a time of 0, a user
  and a group of 0, no user name and no group name. A PAX extended header
  holds only the records `path` and `linkpath`, and only when a value
  does not fit the USTAR fields.
- The entry types are regular file and symbolic link only. A hard link, a
  directory, a character device, a block device, a FIFO, a sparse file and
  every other type are errors.
- The writer sets fixed mode bits: `0644` for a regular file, `0755` for an
  executable file, `0777` for a link.
- The entries are in the order of the manifest. The tar file ends with
  the two zero blocks of the format. A byte after them is an error.

**Manifest values.**

- A size is a JSON integer from 0 to 2^53 − 1, so that RFC 8785 writes it
  in one form.
- Each hash is SHA-256 in lowercase hex, with no prefix. The digest of the
  file set has the prefix `sha256:`.
- A mode is one of the strings `file`, `executable` and `symlink`.
- Entries are in the order of the bytes of their paths.
- The hash of the tar file is the hash of its plaintext, before the server
  seals it.

**The canonicalizer.** `pkg/catalog/jcs.go` writes RFC 8785 today. We
propose to move it to shared code that `pkg/catalog` and `pkg/source` both
import, for example `internal/jcs`, so that `pkg/source` never imports
`catalog`.

### 4. Mounts

A mount names its snapshot by a reproducible key, never by the hash of a
pack: the forge connection, the repository, the commit and the hash of the
source policy, as ADR 135 decision 1 keys a source snapshot. The manifest
holds this key and the path of the mount. So the digest of the file set
covers the key of each mount, not the bytes of the pack. When the forge
gives a different pack for the same commit, the digest does not change.

The start transaction of the job writes in this order:

1. The renderer makes the file set in memory and the server checks the
   limits.
2. The server writes the manifest and the tar file to the blob store.
3. The transaction writes the record of the file set, with the fence.
4. The transaction starts one `source.fetch` operation for each mount
   that has no snapshot yet (ADR 135 decision 2).

The `source.fetch` operation never changes the digest of the file set, because the manifest
holds only the key of the mount. A blob with no record, after a failed
commit, goes to the orphan cleanup.

### 5. The checks of the launcher

The launcher never writes to the work directory before it checks the
hash of the tar file:

1. It checks the SHA-256 of the manifest against the digest of the job
   spec, and checks the manifest against the rules of section 3 and the
   limits of section 6.
2. It reads the tar file into a private staging area, with mode `0700`,
   outside the work directory. It stops at the size that the manifest
   gives, and at the limit. It checks the SHA-256 of the tar file against
   the manifest.
3. It unpacks the tar file into a staging directory. For each entry, it
   checks the path, the type, the mode and the order against the
   manifest, and the hash and the size of the content as it reads it.
4. It moves the tree into the work directory only when all checks pass.
   On any error, it deletes the staging area and the job fails before its
   first step.

The unpack follows the rules of ADR 139 decision 2 (proposed): it writes
through an `os.Root` on the staging directory, it creates each regular
file with `O_CREATE|O_EXCL|O_NOFOLLOW`, and it writes each link with
`Root.Symlink` after the checks of section 3. The launcher then writes the
mounts at their paths.

### 6. Limits

We propose limits for each pool, as ADR 139 decision 7 (proposed) gives
for images. Each pool can change each limit. An entry is one regular file
or one symbolic link of the manifest. Directories are not entries.

| Limit | Checked | Default |
| --- | --- | --- |
| Size of the manifest | before the server writes it, and before the launcher reads the tar file | 4 MiB (4,194,304 bytes) |
| Entries | the same | 10,000 |
| Total content bytes (the sum of the sizes) | the same, and while the launcher unpacks | 64 MiB (67,108,864 bytes) |
| Size of one file | the same | 16 MiB (16,777,216 bytes) |

A rendered root module is a few KiB, so these limits leave room for an
upload of RFC 72. The limits of ADR 135 decision 8 still apply to the
whole checkout, mounts included. The server applies the limits of the
pool of the job before it writes a blob.

A file set that passes a limit fails the job before its first step. The
error names the digest, the limit, its value and the pool setting that
changes it, for example: `File set sha256:65801d79252f... has more than
10,000 entries. Set a larger fileSet.maxEntries on the pool, or render
fewer files.` The setting name is a placeholder.

### 7. Sealing

The server seals the tar file with the data key of the namespace, as ADR
135 decision 10 does. A producer that can put a secret in a file set (none
today) seals it with an item key of its own, so that a redaction can shred
it.

The digest is the hash of the plaintext manifest. This has a cost: the
manifest names each path and the hash of each file, so a person who sees
a digest or the manifest can test a guess of the content of a small file.
A hash of the sealed bytes would hide this, but the digest would then
change with each seal and would not be reproducible (constraint 3). We
propose to keep the plaintext digest and to seal the manifest in SQL with
the same key as the tar file. A reader then needs the key to see the
paths and the hashes, and the job spec shows only the digest.

### 8. Backup and retention

- The record of a file set, with its sealed manifest, is in SQL and so in
  the backup.
- We propose that a file set becomes a history item of a new kind,
  `file-set`, with the digest as its item ID (ADR 150 decision 1). This
  needs an amendment of ADR 150 decision 1, which lists the kinds
  `state`, `plan` and `job-log`.
- The tar file stays in the blob store, and in the backup, until the
  latest expiry time of the first version of the job log of each job of
  the unit that names the file set (ADR 152 decisions 3 and 4). The rule
  uses the first version because a substitution writes a new version with a new
  expiry time, and a substitution must not extend the life of the files.
- The end of that period is a redaction of the `file-set` item, on the
  path of ADR 152 decision 5: the actor `@server`, the cause `retention`,
  and an audit entry with the action `unit:redact`. The redaction deletes
  the tar file and keeps the manifest.
- The purge of the unit deletes the manifest and the tar file.
- A job after a restore that needs a deleted tar file renders again. When
  the new digest is equal, the server writes the tar file again. When it
  is different, the job fails with `source.ErrFileSetDigestMismatch`,
  which names the old digest and the new digest. The server never
  replaces a file set in silence.

### 9. Code

The format, the checks and a conformance suite (`sourcetest`) live in
`pkg/source`, with no import of `catalog`. The canonicalizer moves to
shared code (section 3).

### 10. History and the API

A history item already records its job (ADR 150 decision 3). The job
names the digest. We propose two routes in the job API, for example:

| Operation | Route | Action | Object |
| --- | --- | --- | --- |
| Read the manifest of the file set of a job | `GET /api/jobs/{job}/files` | `job:read-files` | unit |
| Read the content of one file | `GET /api/jobs/{job}/files/{path}` | `job:read-files` | unit |

`job:read-files` is a new action, like `job:read-logs` (ADR 152 decision
10). The console shows the manifest, each file and the diff of two
manifests through these routes only (ADR 17). A redacted tar file answers
410 for the content of a file, and the manifest stays readable.

## Open questions

- **The term.** `GLOSSARY.md` says that "snapshot" is the source snapshot
  only, and ADR 110 says "rendered snapshot". Is a file set a kind of
  source snapshot, or a term of its own? The glossary changes when an ADR
  decides this.
- **The action.** Is `job:read-files` a new action, or does
  `job:read-logs` cover the files too? Which built-in roles get it?
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
- **A git-compatible ID in `DUSTPAN_COMMIT`.** Does the launcher put a
  git tree or commit ID of the file set in `DUSTPAN_COMMIT`, so that tools
  that read the variable get a value? Or does the variable stay empty?
- **Mount paths.** Which path below the work directory does a module
  snapshot get, and can two mounts share a path prefix? Can a mount path
  be equal to a file of the set, or be below it or above it? We propose
  that each of these is an error, but the rule is not decided.
- **Renderer upgrades.** A new renderer version can give new bytes for the
  same revision. Does a rerun of an old revision use the stored file set,
  or render again and show the diff before it runs?
- **RFC 54.** Can a bucket source and an OCI source be file sets that the
  server gets, as the `source.fetch` operation gets a git pack? Then RFC 54
  needs only a fetcher for each source, not a format.

## Outcome

Empty until the project owner accepts or drops the RFC.
