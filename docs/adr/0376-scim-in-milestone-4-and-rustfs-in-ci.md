# 376. SCIM comes in milestone 4, and CI runs the S3 suite on RustFS

Status: accepted · 2026-11-14. Records decisions of the project owner of 14 November 2026, in the owner's words

Changes:

- Changes ADR 332 decision 9: a wording refinement with the owner's word, SCIM comes in milestone 4.
- Changes ADR 31 decision 8: a wording refinement with the owner's word, the owner said yes to RustFS 1.x in CI, pinned by digest.

## Context

Two OpenSpec changes stopped on a question that only the project owner can
answer.

- `scim` builds SCIM 2.0 provisioning. Its task 2.1 stops all build work
  until the owner says when SCIM comes. ADR 332 decision 9 (owner,
  2026-11-07) says: "SCIM comes later (owner, 2026-11-07). This ADR adds no
  SCIM. When SCIM comes, it uses the same mapping, as ADR 320 decision 13
  says." The words do not say how much later. The exit test of milestone 4
  in `docs/roadmap.md` already has a SCIM step: "A SCIM 2.0 client creates a
  user and a group [...] The next CLI command fails with 401."
- `ci-service-container-s3-suite` runs the S3 suite of the blob port in CI.
  ADR 31 decision 8 says: "The planned image is RustFS, pinned by digest.
  This is a new test tool in CI, so it waits for the project owner's yes".
  RustFS (https://github.com/rustfs/rustfs) is Apache-2.0. Its version 1.0.0
  is from 16 September 2026, and 1.0.x patch releases follow it.

The owner answered on 14 November 2026, in the batch of gated changes. The
owner said: "1.A / 2.A / 3. A / 4. B [...] / 5. A". Item 1 option A is "Allow
SCIM in milestone 4 now, with the mapping of ADR 320". Item 5 option A is
"Yes to RustFS 1.x, pinned by digest".

Both decisions edit the words of the earlier decision in place, with a
changelog entry and the owner's word (ADR 365 decisions 2 and 3).

## Decision

1. **SCIM comes in milestone 4 (owner, 2026-11-14).** The owner said: "1.A".
   SCIM 2.0 provisioning is built in milestone 4, with the group mapping of
   ADR 332 and ADR 320 decision 13. ADR 332 decision 9 now says so. The gate
   of task 2.1 of the change `scim` is lifted.
2. **CI runs the S3 suite on RustFS 1.x, pinned by digest (owner,
   2026-11-14).** The owner said: "5. A". The service container of ADR 31
   decision 8 is RustFS 1.x. The image is pinned by its digest. The agent
   that starts the work reads the digest of the current 1.x release again,
   and pins that digest. ADR 31 decision 8 now says so.

## Consequences

- The change `scim` is free to start when its other dependencies are on
  `main`. Its spec keeps the rules of ADR 55 and ADR 70 decision 19 for a
  removal.
- The digest in the consequences of ADR 31 may be from before RustFS 1.0.
  The work does not use it without a new check.
- The edit of `.github/workflows/ci.yml` for the service container still
  follows the rules for workflow files. This ADR does not change them.
