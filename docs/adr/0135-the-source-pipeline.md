# 135. The source pipeline: snapshots of git packs

Status: accepted · 2026-10-18 · supersedes the "Source" item of RFC 6 · adds to ADR 100 decision 5 · ADR 199 adds file sets. Decision 6 extended by ADR 213 decision 2. The name of decision 6 superseded by ADR 226: the source policy is the source filter

## Context

[RFC 6](../rfc/0006-runners-and-remote-runs.md) proposes that the runner
checks out the source itself, with a short-lived read-only token from the
server or with git credentials of its own.
[ADR 100](0100-a-shared-job-model-on-durable-operations.md) decision 5
fixes the commit of a job at start. [RFC 54](../rfc/0054-sources-beyond-git.md)
asks about sources that are not git.

The project owner decided on 17 October 2026 (owner):

- The source fetcher of the server streams the raw git pack to object
  storage. The runner checks it against the commit and checks it out.
  OpenDustpan does not use go-git.
- A snapshot holds submodules and Git LFS objects only under an allowlist
  policy, and the server gets them.
- Git gets custom credential helpers, with a mode "none".

The project owner approved every decision of this ADR. The label "(owner)"
names a decision that the text of the owner of 17 October 2026 states.
The ports are in `pkg/source`. This ADR uses
the verb "get" for the work of the source fetcher.

## Decision

1. **A source snapshot (owner).** A **source snapshot** is the code of one
   commit of one repository in the blob store: the raw git pack, with the
   commit and every tree and blob that it reaches, and the allowed
   submodules and Git LFS objects. The key of a snapshot is the namespace,
   the forge connection, the repository, the commit and the hash of the
   source policy (`source.SnapshotKey`). Two jobs on one commit with one
   policy share one snapshot.
2. **The server gets the pack (owner).** At the start of a runner job, the
   job store starts one durable operation of the kind `source.fetch`, with
   the snapshot key as its start key, in the transaction of the start. The
   operation:
   - speaks the git protocol over HTTPS to the forge, with the credentials
     of the forge connection, and asks for the one commit with depth 1
     (`source.Fetcher`),
   - streams the raw pack to the blob store as it arrives, with its
     SHA-256, and never writes a work tree,
   - reads the submodules and the Git LFS pointers of the commit from the
     pack, gets the allowed ones (decision 6), and records the snapshot
     ready (`source.Store.Complete`).

   No git library with a work tree runs in the server. The server needs
   the pack format only to read `.gitmodules`, the gitlinks and the
   pointer files of the commit.
3. **The runner never connects to the forge for the code.** The launcher
   reads the snapshot through its session (ADR 131). A snapshot that is not
   ready answers `source-not-ready` with `Retry-After`. The launcher waits
   up to 10 minutes. A snapshot that failed fails the phase with the reason
   of the `source.fetch` operation, for example an unknown commit. So the forge credentials
   stay in the server, and the network of the team needs no route to the
   forge.
4. **The launcher checks the pack (owner).** Before it writes a file, the
   launcher checks (`source.Checkout`):
   - the SHA-256 of the pack is the hash of the snapshot,
   - the hash of each object is its ID, in the object format of the
     repository, SHA-1 or SHA-256, after the resolution of each delta,
   - the commit of the job is in the pack, and every tree and blob that the
     commit reaches is in the pack,
   - no path has a part `..`, a part `.git` in any case, or a NUL byte, and
     no path leaves the work directory,
   - a symbolic link points inside the work directory.

   For each allowed submodule, the launcher checks its pack in the same way,
   and checks that its commit is the commit of the gitlink at its path in
   the tree of the superproject that it checked before
   (`source.Checkout.Submodule`, `source.CheckGitlink`). For each allowed
   Git LFS object, it reads the object through its session, checks its
   SHA-256 and its size against the pointer file in the checked tree, and
   only then replaces the pointer file (`source.Checkout.LargeFiles`,
   `source.CheckLargeFile`).

   A check that fails ends the launcher with exit code 68 before the first
   step. The launcher reads the pack format with its own code, from the
   standard library, with no go-git.
5. **No `.git` directory.** The launcher writes the files of the commit,
   with their modes: regular, executable and symbolic link. It writes no
   `.git` directory. The commit is in `DUSTPAN_COMMIT`. A tool that needs
   git metadata is an open question.
6. **The source policy (owner).** A namespace has a source policy
   (`source.Policy`). It is empty by default: a snapshot holds no
   submodule and no Git LFS object.
   - `submodules` lists URL patterns. The server gets a submodule whose URL
     matches, at the commit that the gitlink pins, as a pack of its own.
   - `largeFiles` lists URL patterns of repositories. The server gets the
     Git LFS objects of a matching repository, and checks the SHA-256 and
     the size of each one against its pointer.
   - `failOnDenied` fails the `source.fetch` operation when the commit has a submodule or a
     pointer that the policy does not allow. Without it, the checkout
     holds the empty directory of the gitlink or the pointer file as it is.

   A URL pattern is an `https` URL of a repository with no user, port,
   query, fragment or `%`. Its host has only ASCII letters, digits, `.`
   and `-`, with no empty label, so a host with a letter of another
   script that looks like an ASCII letter cannot be a pattern. A `*` can
   be the whole last path part, and matches one part
   (`source.URLPattern`). A URL to match with a `%`, with such a host, or
   with a path part that is empty, `.` or `..`, matches no pattern, so an
   encoded `/` or `.` cannot pass a pattern. One `/` at the end of a URL
   does not count: `https://h/a/b/` is `https://h/a/b`. The server
   refuses `//` at the end. A policy has at most 64 patterns in each
   list. A change of the policy needs `namespace:manage-runners`, and makes
   a new snapshot key.

   The server applies the policy with these rules:
   - A relative URL in `.gitmodules`, which starts with `./` or `../`,
     resolves against the URL of the superproject before the match, as
     git resolves it (`source.ResolveURL`). One `/` at the end of the URL
     of the superproject or of the submodule does not count, and the
     result has no `/` at its end. The server refuses a relative URL that
     leaves the host of the superproject, and a URL with an empty path
     part.
   - When the server gets a submodule or a Git LFS object, it follows no
     redirect to another host.
   - The server sends the credentials of the forge connection only to the
     host of the connection (`source.SameHost`). It gets an allowed
     submodule of another host with no credentials.
   - The server gets Git LFS objects only from the standard Git LFS
     endpoint of the repository on the forge, `<repository>.git/info/lfs`
     (`source.LFSEndpoint`), with one `/` at the end of the repository URL
     taken off first. It never reads `lfs.url` from a `.lfsconfig`
     file of the commit.
7. **Git credentials of a step (owner).** A tool that runs git during a
   step, for example to get an OpenTofu module from a git URL, gets the
   git credential mode of its pool (ADR 130 decision 11): `none`, `forge`
   or `custom`. The mode `none` is the default.
8. **Limits.** A pack has at most 2 GiB, a checkout at most 8 GiB and
   500,000 files, a snapshot at most 64 submodules and 10,000 Git LFS
   objects, and a Git LFS object at most 2 GiB. A limit fails the
   `source.fetch` operation or the checkout with `source.ErrTooLarge`.
9. **Retention.** The blobs of a snapshot are in the blob store under
   `source/<snapshot ID>/`. The server deletes them 7 days after the end of
   the last job that names the snapshot. A snapshot holds code, not
   history: the history model does not keep it, and a backup of the
   namespace does not hold its blobs. A job after a restore gets a new
   snapshot.
10. **Encryption.** The server writes the blobs of a snapshot with the data
    key of the namespace, as other namespace data, because code can hold
    names and text of the team.

## Consequences

- The forge sees one request for the pack of each commit and policy, from
  the server, not one clone for each runner.
- A runner can run in a network with no route to the forge.
- The server needs storage for the packs of the commits of the last 7 days
  of jobs. A shallow pack of one commit is usually much smaller than a
  clone.
- A tool that needs git history or a `.git` directory does not work as a
  step without a change.
- The server holds code for 7 days, which RFC 6 avoided. The owner's
  decision accepts this.

## Open questions

- Sources that are not git (RFC 54): an OCI artifact or a bucket can be a
  snapshot of its own kind, with the same check by hash. This design keeps
  the port open for them and decides nothing.
- Does the launcher write a minimal `.git` directory, with `HEAD` and the
  commit only, for tools that read git metadata?
- Does a snapshot hold the packs of submodules that a policy denies when
  `failOnDenied` is off, so that a later change of the policy needs no new
  `source.fetch` operation? This design gets only what the policy allows.
- Does a namespace get one policy, or does a unit get its own, below the
  namespace policy?
