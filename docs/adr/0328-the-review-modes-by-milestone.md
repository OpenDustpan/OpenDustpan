# 328. The review modes by milestone

Status: accepted · 2026-11-05. Decided by the project owner on 1 November 2026

Changes:

- Extends ADR 299 decision 1: modes 2 and 4 ship in milestone 5, and modes
  1 and 3 ship in milestone 6.

## Context

ADR 299 decision 1 gives four review modes for a Git host connection:

- mode 1, merged approvals,
- mode 2, OpenDustpan merges,
- mode 3, no review requests,
- mode 4, no change orders.

ADR 299 does not say in which milestone each mode ships. The PM asked the
owner on 1 November 2026 to put modes 2 and 4 in milestone 5, and modes 1
and 3 in milestone 6. The owner accepted this on 1 November 2026, at about
02:10 UTC. Until this ADR, the answer was only in the record of owner
decisions that the PM keeps.

## Decision

1. **Modes 2 and 4 in milestone 5 (owner).** The exit test of milestone 5
   proves mode 2 and mode 4.
2. **Modes 1 and 3 in milestone 6 (owner).** The exit test of milestone 6
   proves mode 1 and mode 3.

## Consequences

- This ADR orders the work. It does not remove a mode, and it does not
  change the other decisions of ADR 299.
- `docs/roadmap.md` lists RFC 114 in milestone 5 with modes 2 and 4, and in
  milestone 6 with modes 1 and 3.
- Until milestone 6 ships, mode 1 and mode 3 are not available. This ADR
  does not decide how the server answers a request for them before that.
