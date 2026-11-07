# 345. An artifact version has four states

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 269 decision 11: the life cycle of a version has four states.

## Context

ADR 269 decision 11 gives a version of a private provider or module two life-cycle actions. A "yank" takes a version out of the index and keeps it downloadable, so a lock file still works. A "revoke" blocks the version. Both keep the record.

On 7 November 2026 the owner decided the states below.

## Decision

Decided by the project owner on 7 November 2026.

1. **Four states (owner, 2026-11-07).** The state of an artifact version is `published`, `deprecated`, `retracted` or `revoked`.

## Consequences

- This ADR does not decide what each state does to the index and to the download. It does not decide which state replaces the "yank". It does not decide the transitions. It does not decide which action and which audit entry change a state.
- ADR 269 decision 11 keeps its meaning of "revoke": the version stays blocked.
- This ADR does not decide the API values. It does not decide if the states apply to artifacts other than providers and modules.
- A page of the user documentation for the private registry needs an update when the feature lands.
