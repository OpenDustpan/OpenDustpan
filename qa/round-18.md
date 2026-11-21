# QA round 18

Range: 4daa46bceabe..909ee0bd425b. Local run: SQLite, local file blob store,
TLS on loopback, binary built to /var/tmp. No `tofu` or `pulumi` binary was
available, so state was written through the TFE-compatible API with curl.
No secret is in this file.

## Paths that worked as the docs say

- Quickstart: build, serve, namespace, collection, placement rules.
- First administrator: local user, set-up code, password sign-in, redeem.
- Capabilities document: `stateWriteGates` matches the guide (14 checked, 2 unchecked).
- Masked state read: first page, cursor page (200 markers per page), 400 for a
  bad version, a cursor of another version, a cursor that is too long, a query
  that is not allowed. 404 for a unit that does not exist. A cursor ends after a
  server restart, as the guide says.
- Backup with `--backup-target dir:`: `backup`, `backup check`, `restore
  --check-only`. Wrong or missing `--backup` gives a clear message.
- Gate configuration routes at the namespace, collection, unit and installation
  scopes: gates, access limits, approval rules. Token change gives 403
  `mfa.fresh-step-required`. Cookie without `Origin` gives `session.other-site`.
  `gate.final` and `gate.version-conflict` are as documented.
- SDK reference example builds and answers `/api/user`. A short token gives the
  documented error.

## Findings (issues)

- #3915 Masked read marks every attribute, not only the sensitive paths.
- #3916 PUT of approval rule or default on a missing unit answers 422, not 404 `unit.not-found`.
- #3917 SDK example panics with no directory argument.

## Not tested

- Damaged-job rows in `backup check` (needs a damaged database row).
- Job approvals with several groups, `connect-github`, runner API changes
  (need runners or a GitHub host).
