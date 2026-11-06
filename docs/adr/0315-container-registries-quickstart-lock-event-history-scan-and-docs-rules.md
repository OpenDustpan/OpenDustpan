# 315. Container registries, the quickstart, the lock event, the history scan and the rules for the docs site

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 and 19 October 2026

Changes:

- Extends ADR 283: the container registries of v0.1.0.
- Extends ADR 238: the quickstart.
- Extends ADR 145: the event `lock.held-too-long`.
- Extends ADR 214: who owns the pages of the docs site.
- Extends ADR 74 decision 6: no visit counting on the docs site.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 and 19 October. A check on 1 November 2026 found no ADR that states them.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Container registries of v0.1.0 (owner, 2026-10-18).** The owner said: "Distribution for v0.1.0: forge container registry plus Docker Hub (organization already registered)".
2. **The quickstart (owner, 2026-10-18).** The owner said: "Quickstart: both, `opendustpan serve` with SQLite on loopback, and a compose file with PostgreSQL and S3-compatible storage".
3. **The lock event (owner, 2026-10-18).** The owner said: "Add `lock.held-too-long` now (namespace limit, default 1 hour)". The owner said also: "the drift event comes with the drift ADR".
4. **The history scan (owner, 2026-10-19).** After the history rewrite, the owner said: "trufflehog keeps scanning the full history".
5. **Docs ownership (owner, 2026-10-18).** The owner said: "Docs ownership: each change's author owns its pages". The owner said also: "one maintainer owns structure and voice".
6. **No visit counting (owner, 2026-10-18).** The owner said: "No visit counting on the docs site".

## Consequences

- ADR 74, ADR 145, ADR 214, ADR 238 and ADR 283 get a pointer to this ADR.
- Decision 3 needs a test for the event with the limit and with the default of 1 hour.
