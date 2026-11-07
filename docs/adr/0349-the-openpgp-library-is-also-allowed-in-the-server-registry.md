# 349. The OpenPGP library is also allowed in the server registry

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 316 decision 1: the OpenPGP approval is not only for the provider kit.
- Extends ADR 316 decision 2: the approved library versions apply in the server registry.

## Context

ADR 316 decision 1 records that the owner approved OpenPGP signing for `opendustpan/providerkit` only, and not for the server. ADR 316 decision 2 records the approval of an OpenPGP library, with versions, after a provenance check. The server registry (ADR 269) checks the signature of a release, so it needs the same library.

On 7 November 2026 the project owner decided that the server registry may use the library too.

## Decision

Decided by the project owner on 7 November 2026.

1. **The server registry may use the OpenPGP library (owner, 2026-11-07).** The server registry may use the OpenPGP library of ADR 316 decision 2. It uses the library to check signatures of private providers and modules.
2. **The same versions (owner, 2026-11-07).** The versions of ADR 316 decision 2 apply. A new version or a new library needs a new approval.

## Consequences

- This ADR does not approve any other dependency for the server. The rule "ask before adding a dependency" stays.
- The scope "for opendustpan/providerkit only" of ADR 316 decision 1 does not apply to OpenPGP signing now. It still applies to the other dependencies of that decision.
