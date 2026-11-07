# M5-60b: guide: run an apply on a runner and approve it

- Class: MECHANICAL (docs only), with a SIZED fallback if the guide finds a gap
- Routing: Haiku; route to Sonnet if the steps do not run as written
- Branch: `m5-60b-guide-runner-apply`
- Conflict group: `docs-runners`
- Persona path: P5 runner apply ends here: the API output, the console run
  page, and this guide.
- Depends on: #2611 (M5-8a), M5-8b and M5-13a. NONE IS ON MAIN at
  9e96ae30c8c3. Do not dispatch until all three merge. Re-check the facts
  below on that main.

## Facts checked at main 9e96ae30c8c3

- `docs/guides/` has no runner guide. `docs/guides/approval-rules.md` exists.
- `docs/reference/runner-api.md` documents pools (line 38), join tokens (62),
  runners (71), the runner protocol (83), the job session (152).
- `docs/reference/jobs-api.md:10` says the API does not approve yet; line 77-78:
  a plan gets no approval gate.
- `docs/reference/launcher.md` exists.
- ADR 214: a user-visible feature has its page in the same PR; this guide is
  the task page of the P5 path.

## Files it may touch

- `docs/guides/runners-and-job-approvals.md` (new)
- `docs/guides/approval-rules.md` (one link to the new guide)
- `README.md` or the guide index, one link, if the docs keep an index

## Files it must not touch

- Any code, `docs/reference/*` (each code item updates its own reference page)

## Change

A task guide, in STE-flavored English (`asd-ste100` skill), with commands
that a reader can copy:

1. Make a runner pool and a join token (`docs/reference/runner-api.md`).
2. Join a runner.
3. Add an approval rule that needs two people to the unit (`approval-rules.md`).
4. Start an apply job through the API; read the job: `awaiting-approval`.
5. Two people approve (`POST .../jobs/{job_id}/approvals`); a destroy approval
   needs a fresh MFA step.
6. Read the log, the stored plan link, the decision record, and the unit lock
   in the inventory while the apply phase runs.
7. What fails and the one command that helps: a stale plan, a self approval,
   a Pulumi destroy whose resources changed.

Use the exact field names and error codes of the merged code. Do not
describe a behavior that the code does not have.

## Acceptance

- Each command in the guide runs against `opendustpan dev` on the merged main;
  paste the output in the PR.
- `make docs-lint` reports no new finding for the new file.
- No internal reference (ADR, RFC, issue or PR number) in the guide.
