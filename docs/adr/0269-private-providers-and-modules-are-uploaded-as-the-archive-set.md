# 269. Private providers and modules: own providers first, uploaded as the archive set

Status: accepted · 2026-10-18 · decided by the project owner. The open design questions are in RFC 101

## Context

A team builds its own OpenTofu providers and modules (ADR 170). ADR 177 gives
the archive set of a provider release: the zip archives, the checksums file
and its detached signature. ADR 257 decides that OpenDustpan serves
providers and modules through the HTTP protocols of the tools, and serves no
OCI endpoint.

Those ADRs do not say which artifacts come first, or how an artifact gets
into OpenDustpan. The project owner decided both on 2026-10-18. The decisions
are in the section "Decided" of issue 949.

This ADR records only those decisions. It does not decide the routes, the
storage, the signing keys, the authorization actions, the limits or the
module sources. RFC 101 holds those questions.

## Decision

1. **Own and team providers first.** Issue 949, "Decided", items 10.1 and
   10.2 (owner, 2026-10-18):

   > own and team providers first, through the OpenTofu provider network
   > mirror protocol. An upstream proxy comes later.

2. **An upload through the API.** Issue 949, "Decided", item 10.4:

   > private providers and modules are uploaded through the API as the
   > archive set of ADR 177. Checksums and the signature are checked at
   > upload.

## Consequences

- The first served artifact is a provider that a team built. The upstream
  proxy and the curation of public packages (RFC 10, RFC 93) come later.
- A feature has no OCI path, no git tag source and no signature policy from
  this ADR. RFC 101 proposes them, and each one needs its own decision.
- A feature of the registry has a REST API (ADR 17), and the upload is part
  of it. No feature works only through a CLI or a configuration file.
