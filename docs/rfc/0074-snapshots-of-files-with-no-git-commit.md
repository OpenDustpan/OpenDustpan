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

1. **Addressed by content and immutable.** The digest of a snapshot is a
   hash of its content. Two writes of the same content give the same
   digest. No
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
| 5. Sealed | Item key for the tar, data key for the manifest | Data key or item key, and the repository on the server too | Data key or item key |
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

**Two writes of one key.** Two jobs of one unit can render the same file
set at the same time. The server names each blob by a random ID, never by
the digest, so the two writes never write to the same blob. Each job
inserts the record of the key. An insert that meets an existing record of
the same key is a success: the job uses the existing record and its
blobs. The blobs of the job that lost have no record, and go to the orphan
cleanup (ADR 151 decision 22).

### 2. The job spec

`job.Location` of the kind `catalog` names the digest (ADR 110 change 3).
The launcher gets the digest from its job session and puts it in
`DUSTPAN_SNAPSHOT`. `DUSTPAN_COMMIT` stays empty (see the open questions).

This amends ADR 134 in two places:

- Decision 6 lists the variables of the launcher. `DUSTPAN_SNAPSHOT` is a
  new variable in that list.
- Decision 4 step 4 gets the source snapshot and checks it. For a file
  set, step 4 gets the file set, does the checks of section 5 and writes
  the files. A failure ends with exit code 68, as for a git snapshot.

### 3. The exact canonical form

`pkg/source` writes the manifest and the tar file. A renderer adapter
gives only paths, modes, link targets and bytes. The writer refuses an
input that breaks a rule below. The launcher refuses a file set that
breaks a rule below. A test writes the same file set twice and compares
the digests.

**Paths.**

- The separator is `/`. A path has no leading `/` and no trailing `/`.
- No part of a path is empty, `.` or `..`.
- A part of a path is at most 255 bytes. A path is at most 4,096 bytes
  and has at most 64 parts.
- A path has no control character (U+0000 to U+001F and U+007F to U+009F)
  and no `\`.
- A path is valid UTF-8 in Unicode Normalization Form C (NFC). A path that
  is not in NFC is an error. The writer does not normalize it.
- Two paths that are equal after Unicode full case folding are an error,
  because a file system that folds case writes them to one file.
- No path, after full case folding, is equal to a proper prefix of
  another path after full case folding. A proper prefix ends at a `/` of
  the other path. So the file `a` and the file `a/b` are an error, and the
  file `A` and the file `a/x` are an error too.
- A duplicate path is an error.

Full case folding is the mapping of the Unicode file `CaseFolding.txt`
with the status C and F. NFC and case folding need the module
`golang.org/x/text` as a direct dependency of `pkg/source` and of the
launcher. Today it is an indirect dependency (v0.42.0). The project owner
must approve this change (`CLAUDE.md`: ask before you add a dependency).
The module selects its Unicode tables by the Go version: v0.42.0 with Go
1.27 or later uses Unicode 17.0.0. An upgrade of the module or of Go that
changes the Unicode version can change which paths are valid. So the
Unicode version is part of the format, and a test checks it.

**Directories.** The manifest and the tar file have no directory entries.
A directory exists only as a parent of a file or a link, so a file set has
no empty directory. The launcher makes each parent directory with mode
`0755`.

**Symbolic links.**

- A target is relative. An absolute target is an error.
- A target is in normal form: no empty part, no `.` part, and `..` only
  at the start.
- A target is at most 4,096 bytes. It follows the rules of a path for the
  characters, UTF-8 and NFC: no control character, no `\`, and valid UTF-8
  in NFC. A target that is not in NFC is an error. The writer does not
  normalize it.
- The target, resolved from the directory of the link, is a regular file
  or a directory inside the file set. A target that leaves the file set or
  names nothing in it is an error.
- A target is never the root of the file set, and never the link itself
  or a directory above the link. So a link never makes a loop.
- A target is never a link, and no part of the resolved path is a link.
  So no chain goes through a link.
- No path of the file set is below the path of a link. For example, the
  link `m` and the file `m/x` are an error.
- A target never resolves at or below a mount path. The check compares
  the resolved target and the mount path after full case folding.

**Tar.** `pkg/source` owns the exact bytes of each header. It does not
use the encoder of `archive/tar`, because that encoder can change between
Go versions. Golden test vectors in `sourcetest` hold the exact bytes of
a set of tar files, and the writer must give these bytes.

- The format is USTAR. Each entry has one USTAR header. An entry has a
  PAX extended header (typeflag `x`) before its USTAR header only when its
  path or its link target is not ASCII or is more than 100 bytes.
- A PAX extended header holds only the records `path` and `linkpath`, in
  this order, and only the records that the entry needs. Each record has
  the form `<length> <key>=<value>\n`, where the length is decimal and
  counts the whole record.
- The PAX extended header has the name `PaxHeader`, the mode `0644`, the
  size of its records, and the other fields as below.
- A USTAR header has these fields:
  - The name: the path in ASCII, or empty when a PAX record holds the
    path. The prefix field is always empty.
  - The mode: `0644` for a regular file, `0755` for an executable file,
    `0777` for a link.
  - The user ID 0, the group ID 0, the time Unix 0 and the size. A link
    has the size 0.
  - The typeflag: `0` for a regular file, `2` for a symbolic link. The
    writer never writes the typeflag NUL. `archive/tar` reads the
    typeflag NUL as `0`, so two tar files that differ only in this byte
    give the same entries. The hash of the tar file pins the bytes, and a
    golden vector shows the typeflag `0`.
  - The link name: the target in ASCII, or empty when a PAX record holds
    the target, or empty for a regular file.
  - The magic `ustar\0` and the version `00`.
  - An empty user name and an empty group name, and the device numbers 0.
- Each number field is octal with leading zeros, in the full width of the
  field minus one byte, and ends with a NUL byte. The checksum field is
  six octal digits, a NUL byte and a space. Unused bytes are NUL.
- The entries are in the order of the manifest. The tar file ends with
  the two zero blocks of the format. A byte after them is an error.

The launcher checks each header that `archive/tar` reads:

- `Header.Format` is USTAR, or PAX for an entry that has a PAX extended
  header.
- `Header.PAXRecords` holds no key other than `path` and `linkpath`.
- The typeflag is `0` or `2`. A GNU long name (`L`), a GNU long link
  (`K`), a global PAX header (`g`), a hard link, a directory, a character
  device, a block device, a FIFO, a sparse file and every other type are
  errors.
- The mode, the user ID, the group ID, the time, the user name, the group
  name and the size have the values above.

**Manifest values.**

- `schemaVersion` is the integer 1, and `mediaType` is
  `application/vnd.opendustpan.file-set.manifest.v1+json`. A launcher
  refuses a version or a media type that it does not know.
- `producer` holds the name and the version of the producer.
- `tar` holds the size and the SHA-256 of the tar file.
- An entry of a regular file holds its path, its mode, its size and its
  SHA-256. An entry of a link holds its path, its mode and its target, and
  has no size and no SHA-256. The tar header of a link gives the size 0.
- A mount holds its path and the key of its snapshot (section 4).
- A size is a JSON integer from 0 to 2^53 − 1, so that RFC 8785 writes it
  in one form.
- Each hash is SHA-256 in lowercase hex, with no prefix. The digest of the
  file set has the prefix `sha256:`.
- A mode is one of the strings `file`, `executable` and `symlink`.
- Entries are in the order of the bytes of their paths. Mounts are in the
  order of the bytes of their paths.
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

A mount path follows the path rules of section 3. No mount path is equal
to the path of a link or below it. The comparison is after full case
folding, as for the paths of section 3. The writer refuses a mount that
breaks
these rules, and the launcher checks them in step 1 of section 5. Can a
mount path be equal to, above or below a file or another mount? This
stays an open question.

The start transaction of the job writes in this order:

1. The renderer makes the file set in memory and the server checks the
   limits.
2. The server writes the manifest and the tar file to the blob store,
   each with a random blob ID.
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
   spec, and checks the manifest against the rules of section 3, the
   rules of the mount paths of section 4 and the limits of section 6. The
   implicit directories count against the entry limit, and a path has at
   most 64 parts.
2. It makes a staging directory with mode `0700`. The staging directory
   is a sibling of the work directory below the root directory of the job,
   so it is on the same file system. It reads the tar file to
   `staging/tar`, beside the tree `staging/tree` that step 3 writes. It
   stops at the size that the manifest gives, and at
   the limit. It checks the SHA-256 of the tar file against the manifest.
3. It unpacks the tar file into the staging directory. For each entry, it
   checks the header (section 3), the path, the type, the mode and the
   order against the manifest, and the hash and the size of the content
   as it reads it. It then writes each mount at its path in the staging
   directory.
4. It moves `staging/tree`, and only that directory, to the work
   directory when all checks pass. It uses `renameat2` with
   `RENAME_NOREPLACE` on Linux, or `renamex_np` with `RENAME_EXCL` on
   macOS. The launcher makes no separate check that the work directory
   exists. The errors `EEXIST` and `ENOTEMPTY` are errors, and so is
   `EXDEV`. The launcher never copies the tree instead of the move. The
   launcher deletes `staging/tar` after the move, and it deletes the
   staging directory on any error. The job fails before its first step on
   any error.

The unpack follows the rules of ADR 139 decision 2 (proposed). Check the
references to ADR 139 again when PR #840 merges. It writes
through an `os.Root` on the staging directory, it creates each regular
file with `O_CREATE|O_EXCL|O_NOFOLLOW`, and it writes each link with
`Root.Symlink` after the checks of section 3. The launcher writes each
mount through the same `os.Root`, before the move.

### 6. Limits

We propose limits for each pool, as ADR 139 decision 7 (proposed) gives
for images. Each pool can change each limit. An entry is one regular file
or one symbolic link of the manifest. A directory is not an entry of the
manifest, but each implicit directory (a parent of an entry) counts
against the entry limit. A path has at most 64 parts.

| Limit | Checked | Default |
| --- | --- | --- |
| Size of the manifest | before the server writes it, and before the launcher reads the tar file | 4 MiB (4,194,304 bytes) |
| Entries | the same | 10,000 |
| Total content bytes (the sum of the sizes) | the same, and while the launcher unpacks | 64 MiB (67,108,864 bytes) |
| Size of one file | the same | 16 MiB (16,777,216 bytes) |
| Size of the tar file | the same, and while the launcher reads the tar file | the content limit, plus the entry limit × 10,240 bytes, plus 1,024 bytes: 169,509,888 bytes with the defaults |

The limit of the tar file comes from the other limits. One entry adds at
most 10,240 bytes to its content: a PAX extended header of 512 bytes, its
records of at most 8,704 bytes (a path and a link target of 4,096 bytes
each, with the record overhead, in blocks of 512 bytes), a USTAR header
of 512 bytes, and at most 511 bytes of padding. The two zero blocks at the
end are 1,024 bytes. The manifest holds the size of the tar file, and the
launcher refuses a size above the limit.

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

The server seals the tar file of each file set with an item key of its
own (ADR 151 decision 1). A redaction shreds that key (ADR 151 decision
11), so a server that restores a backup after the redaction cannot read the
tar file.

The server seals the manifest in SQL with the data key of the namespace
(ADR 135 decision 10). The manifest is metadata, as ADR 152 decision 7
keeps the sizes and hashes of a job artifact: it holds paths, modes,
sizes, hashes and link targets, never the content of a file. So the
manifest stays readable after a redaction of the tar file.

The digest is the hash of the plaintext manifest. This has a cost: the
manifest names each path and the hash of each file, so a person who sees
the manifest can test a guess of the content of a small file. A hash of
the sealed bytes would hide this, but the digest would then change with
each seal and would not be reproducible (constraint 3). We propose to
keep the plaintext digest. A reader needs the data key to see the paths
and the hashes, and the job spec shows only the digest.

### 8. Backup and retention

- The record of a file set, with its sealed manifest, is in SQL and so in
  the backup.
- We propose that a file set becomes a history item of a new kind,
  `file-set`, with the digest as its item ID (ADR 150 decision 1). This
  needs an amendment of ADR 150 decision 1, which lists the kinds
  `state`, `plan` and `job-log`.
- The `source` package owns the `history.Redactor` of `file-set` items,
  as ADR 152 decision 2 gives the job model the `Redactor` of its items.
  It runs the conformance suite `historytest.RunRedactor` with a table of
  file-set situations.
- The tar file stays in the blob store, and in the backup, until the
  expiry time of the file set. The expiry time is the latest of these
  times, for each job of the unit that names the file set:
  - The expiry time of the first version of the job log of the job (ADR
    152 decisions 3 and 4).
  - For a job with no job log: the end of the job plus the job-log
    retention period of the installation at the end of the job
    (`--job-log-retention`).

  The rule uses the first version because a substitution writes a new
  version with a new expiry time, and a substitution must not extend the
  life of the files. A file set does not expire before the end of each
  job that names it, as ADR 152 decision 6 gives for a plan.
- This amends ADR 152 decision 4 for the kind `file-set`. Decision 4
  computes the expiry time once, when the server creates the item. The
  expiry time of a file set grows: each new job that names the file set
  can make it later. It never makes it earlier.
- The end of that period is a redaction of the `file-set` item, on the
  path of ADR 152 decision 5: the actor `@server`, the cause `retention`,
  and an audit entry with the action `unit:redact`. The redaction shreds
  the item key, deletes the tar file and keeps the manifest.
- The purge of the unit deletes the manifest and the tar file.
- The server never writes a redacted file set again. A job that needs a
  redacted file set fails with `source.ErrFileSetRedacted`, which names
  the digest, and the server changes nothing. To run the unit again, a
  person starts a new change, which renders a new file set. When the new
  content is different, the new file set has a new digest and so a new
  key of the unit. When the new content is equal, the digest is equal and
  the server refuses it with the same error.

### 9. Code

The format, the checks and a conformance suite (`sourcetest`) live in
`pkg/source`, with no import of `catalog`. The canonicalizer moves to
shared code (section 3).

### 10. History and the API

A history item already records its job (ADR 150 decision 3). The job
names the digest. We propose two routes in the job API, for example:

| Operation | Route | Action | Object |
| --- | --- | --- | --- |
| Read the manifest of the file set of a job | `GET /api/jobs/{job_id}/files` | `job:read-files` | unit |
| Read the content of one file | `GET /api/jobs/{job_id}/files/{path...}` | `job:read-files` | unit |

`job:read-files` is a new action, like `job:read-logs` (ADR 152 decision
10). The console shows the manifest, each file and the diff of two
manifests through these routes only (ADR 17).

The content route answers:

- 200 with the bytes of a regular file of the manifest. The answer always
  has the headers `Content-Type: application/octet-stream`,
  `X-Content-Type-Options: nosniff`, `Content-Disposition: attachment` and
  `Content-Security-Policy: sandbox`.
- 200 with the manifest entry of a link, which holds its target. The
  answer is JSON with `Content-Type: application/json`. The route never
  follows a link.
- 404 for a path that is not an entry of the manifest. A path at or below
  a mount is not an entry, so it answers 404. The manifest gives the key
  of the mount, and the code of a mount stays in its forge.
- 410 for each entry of a redacted file set. The manifest route still
  answers 200.

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
  that each of these is an error, but the rule is not decided. Section 4
  decides only that a mount path follows the path rules and is never at
  or below a link.
- **Renderer upgrades.** A new renderer version can give new bytes for the
  same revision. Does a rerun of an old revision use the stored file set,
  or render again and show the diff before it runs?
- **RFC 54.** Can a bucket source and an OCI source be file sets that the
  server gets, as the `source.fetch` operation gets a git pack? Then RFC 54
  needs only a fetcher for each source, not a format.

## Outcome

Empty until the project owner accepts or drops the RFC.
