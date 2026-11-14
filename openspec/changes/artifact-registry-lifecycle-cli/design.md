## Context

Release life cycle and read operations sit on the stored registry release. Earlier parts added publish, signature checks, and grants. This part adds state moves and reads.

## Decisions

1. **Life cycle as release state.** The stored release holds one state field. Yank and revoke are state moves only. The index query skips yanked and revoked versions. The download path refuses revoked versions and allows yanked versions. The exact names and allowed moves of the four states of ADR 345 stay open until the owner answers.
2. **API moves, CLI reads.** Yank and revoke run through the management API with the manage grant. Release list, release show, and keys list run with the read grant. Yank and revoke of the CLI are out of scope. Recovery uses list and show with the include flag.

## Structure

Unexported registry release type with a state field. Only the yank and revoke constructors move the state. Each constructor rejects an unknown state.

## Risks

- A client caches the index and keeps asking for a yanked version. Mitigation: the download of a yanked version still succeeds, so a pinned build keeps working.
- A revoked version with a pinned lock file stops working. Mitigation: the 409 error names the version and the list command that shows a replacement.
