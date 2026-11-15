# 370. Housekeeping principals replace the namespace service principal

Status: accepted · 2026-11-14. Decided by the project owner. The product manager chose the stricter shape that the owner allowed

Changes:

- Supersedes ADR 338: the namespace service principal is removed.
- Changes ADR 267 decision 14: no start of a trigger runs as the namespace service principal.
- Changes ADR 369: the point "The namespace service principal" of "Not decided here" is decided.

## Context

ADR 338 (owner, 2026-11-07) gives the namespace service principal a stored
grant of `unit:run-plan` on its namespace. ADR 369 decision 7 (owner,
2026-11-14) gives each automation its own run-as principal, and says that
the namespace service principal has no role in an automation that a person
configures. ADR 369 left one point open: keep the namespace service
principal for the housekeeping of the server, or remove it.

On 14 November 2026 the owner said:

```text
"For ADR338 we can tighten it if you want, as a new ADR and a new set of
changes. We can absorb the blast radius through the SDLC."
```

The product manager chose the stricter shape: remove the principal.

A check of main on 14 November 2026 found:

- The authorizer decides the namespace service principal before every other
  rule, with a rule in code that gives it `unit:run-plan` on the units of its
  namespace (`pkg/access/authorizer.go:119`, `pkg/access/authorizer.go:154`,
  `authorizeNamespaceService`). No stored grant decides it. This is against
  ADR 338 decision 1 and against the rule of `CLAUDE.md` that code must not
  define who holds a role.
- The trigger dispatcher makes the principal for each start
  (`pkg/trigger/dispatcher.go:249`).
- The housekeeping of the server writes its audit entries with the subject
  `@server`, and no grant decides what it may do. Examples: the artifact
  sweep (`pkg/job/artifactsweep.go:617`), the reconcile of jobs
  (`pkg/job/reconcile.go:673`), the cleanup of saved views
  (`pkg/savedview/service.go:292`), the sweep of access tokens
  (`pkg/accesstoken/policy.go:192`), the expiry of temporary
  administrators (`pkg/tempadmin/service.go:365`), the retention of history
  (`pkg/history/history.go:156`) and the locks of local users
  (`internal/localusersvc/locks.go:415`).

## Decision

1. **The namespace service principal is removed (owner, 2026-11-14).** No
   automation of a person and no code path uses it. The authorizer has no
   rule in code for it. This ends the code rule that gives it
   `unit:run-plan` with no stored grant.
2. **Each housekeeping purpose has its own built-in service principal (owner,
   2026-11-14).** The housekeeping that no person configures runs as a
   built-in service principal of its purpose: for example the sweeps of
   blobs and checkpoints, the retention of history, the cleanup of artifacts,
   the sweep of access tokens, the expiry of temporary administrators, the
   cleanup of saved views and the reconcile of jobs. Each one is made at the
   install. The record of each run names it.
3. **Their permissions are stored grants (owner, 2026-11-14).** The install
   step writes the grants of each housekeeping principal as stored grants,
   with a creator and a time. A person can list, audit and revoke them. Code
   defines the roles, never who holds them. A revoke of the grant of one
   housekeeping principal stops only its own work, and the server writes the
   denial to the audit log.
4. **No migration code (owner, 2026-11-14).** No installation exists, so the
   change edits the baseline and writes no migration (ADR 322).

## Consequences

- The run triggers act as the run-as principal of ADR 369 decision 7. The
  read check of the last saver of ADR 267 decision 14 is gone.
- The subject `@server` stays only for the installation as an object of a
  grant. A housekeeping run never acts as `@server`.
- The OpenSpec change `housekeeping-principals` builds this ADR. It lands
  before the changes of triggers, schedules and drift checks of milestones 5
  and 6, or with them.
- The glossary term "Namespace service principal" is removed, and a term
  "Housekeeping principal" is added, in the change.
