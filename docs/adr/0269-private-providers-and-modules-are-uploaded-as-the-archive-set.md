# 269. Private providers and modules: own providers first, uploaded as the archive set

Status: accepted · 2026-10-18 · decisions 1 to 3 by the project owner, decisions 4 to 11 by the product manager (2026-10-20). One design question is open in RFC 101

## Context

A team builds its own OpenTofu providers and modules (ADR 170). ADR 177 gives
the archive set of a provider release: the zip archives, the checksums file
and its detached signature. ADR 257 decides that OpenDustpan serves
providers and modules through the HTTP protocols of the tools, and serves no
OCI endpoint.

Those ADRs do not say which artifacts come first, or how an artifact gets
into OpenDustpan. The project owner decided both on 2026-10-18. The decisions
are in the section "Decided" of issue 949.

Decisions 1 to 3 are owner decisions. Decisions 4 to 11 are decided by the
product manager under the owner's delegation of 20 October 2026. They answer
questions of RFC 101. This ADR does not decide the routes, the authorization actions,
the limits or the OpenPGP library. RFC 101 holds those questions, and the
library stays open for the owner.

## Decision

1. **Own and team providers first.** Issue 949, "Decided", items 10.1 and
   10.2 (owner, 2026-10-18):

   > own and team providers first, through the OpenTofu provider network
   > mirror protocol. An upstream proxy comes later.

2. **An upload through the API.** Issue 949, "Decided", item 10.4:

   > private providers and modules are uploaded through the API as the
   > archive set of ADR 177. Checksums and the signature are checked at
   > upload.

3. **Order of the slices.** The first slice serves providers by upload.
   Decided by the owner (RFC 101 question 1).

4. **Address host.** A private address has the host of the server and the
   namespace of OpenDustpan. The adapter refuses every host except the
   configured host, compared exactly with the configuration. Only a name in
   the registry-safe subset can publish: lowercase letters, digits and single
   dashes, with no dash at the start or the end (RFC 101 question 2).
5. **No shared code with the package mirror yet.** The first archive check
   stays in the registry. It moves when a second package needs it (question 3).
6. **No cross-namespace reads.** A namespace never reads the registry of
   another namespace (question 4).
7. **No re-signing.** The server does not sign what it serves (question 5).
8. **Module sources.** Upload first, git tags second (question 7).
9. **Trusted keys.** Each namespace registers its own signing keys
   (question 8).
10. **Allowlist of SHA-256 sums.** It belongs to the curation of public
    packages, not to this registry (question 9).
11. **Life cycle.** A "yank" takes a version out of the index and keeps it
    downloadable, so a lock file still works. A "revoke" blocks the version.
    Both keep the record (question 10).

## Consequences

- The first served artifact is a provider that a team built. The upstream
  proxy and the curation of public packages (RFC 10, RFC 93) come later.
- A feature has no OCI path and no signature library from this ADR. The
  OpenPGP library needs its own decision by the owner (RFC 101 question 6).
- A feature of the registry has a REST API (ADR 17), and the upload is part
  of it. No feature works only through a CLI or a configuration file.
