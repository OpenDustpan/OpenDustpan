# 363. The store move comes before the namespace ID change

Status: accepted · 2026-11-08. Records one decision of the project owner of 8 November 2026, in the owner's words

Changes:

- Extends ADR 220 decisions 1, 2 and 3: their work starts after RFC 103 step 8.
- Extends ADR 362 decision 2: RFC 103 step 8 also comes before ADR 220 decisions 1, 2 and 3.

## Context

ADR 220 decision 1 gives each row of a namespace a namespace ID. Decision 2 adds composite foreign keys between the rows of a namespace. Decision 3 adds row-level security on PostgreSQL. On `main` in November 2026, the name of a namespace is still a key column of many tables. The work of decisions 1, 2 and 3 is not done.

RFC 103 step 8 moves the store adapters from `pkg/store/` to `internal/store/`. ADR 362 decision 2 puts this step before step 7 of ADR 274.

Both pieces of work change almost every file of the store adapters, in both dialects. The project manager asked the owner which comes first. The options were:

- The store move first. The move is a mechanical change with no change of behavior. The namespace ID change is then written once, against the files in their new place.
- The namespace ID change first. The data model is correct earlier, but each store file changes twice, and other store work conflicts with both changes for longer.

The project manager recommended the store move first.

## Decision

1. **The store move first (owner, 2026-11-08).** The owner answered "Yes lgtm on both, continue as proposed". RFC 103 step 8 comes before the work of ADR 220 decisions 1, 2 and 3. The design of that work can start before the move ends. Its implementation starts after the move.

## Consequences

- The namespace ID change touches each store file once, in `internal/store/`.
- Until the namespace ID change lands, tables keep the namespace name as a key. A new table uses the stable ID of each entity that a user can rename, or remove and make again (ADR 362 decision 1).
