# 394. Housekeeping grants follow each namespace, and each sweep has its own action

Status: accepted · 2026-11-22. Records decisions of the project owner of 22 November 2026, in the owner's words

Changes:

- Changes ADR 370 decision 3: a wording refinement with the owner's word. The install step and each namespace creation write the grants of the housekeeping principals.

## Context

The change `housekeeping-principals-install` stopped on three questions that
only the project owner can answer. The product manager asked them in the
batch of 22 November 2026.

- ADR 370 decision 3 says that the install step writes the grants of each
  housekeeping principal. Five purposes act inside a namespace: the reaper of
  agent runs, the retention of history, the rotation of issuer keys, the
  reconcile of jobs and the cleanup of saved views. A grant on `@server`
  reaches a namespace only when `access.installationGrantsReachNamespaces`
  is `true` (ADR 216 decision 7), and OpenDustpan Cloud locks it `false`. A
  grant that the install writes cannot name a namespace that does not exist
  yet.
- Six sweeps have no action in `pkg/identity/identity.go` on main: the sweep
  of artifacts, the sweep of blobs, the sweep of superseded checkpoints, the
  prune of name locks, the expiry of temporary administrators and the sweep
  of agent reveals.
- The reconcile of jobs continues a job that a person started. It has no
  start action, and `reconcileAction` in `pkg/job/reconcile.go` only names
  the effect in the audit entry.

The owner answered on 22 November 2026: Q1 option C, Q2 option A, Q3 option B.

## Decision

1. **Each namespace creation writes the grants of the housekeeping roles
   (owner, 2026-11-22).** The owner chose option C for Q1. The creation of a
   namespace writes one stored grant on that namespace for each of the five
   roles that act inside a namespace: `housekeeping-agent-reaper`,
   `housekeeping-history-retention`, `housekeeping-issuer-rotation`,
   `housekeeping-job-reconcile` and `housekeeping-saved-view-cleanup`. Each
   grant has a creator and a time. A person can list, audit and revoke each
   one. The install step still writes the grants on `@server`. ADR 370
   decision 3 now says so.
2. **Each sweep has its own narrow action (owner, 2026-11-22).** The owner
   chose option A for Q2. Six new actions in the `server` family:
   `server:sweep-artifacts`, `server:sweep-blobs`,
   `server:sweep-checkpoints`, `server:prune-name-locks`,
   `server:expire-temporary-administrators` and
   `server:sweep-agent-reveals`. Each role of a sweep holds one of them.
3. **The reconcile of jobs has its own action (owner, 2026-11-22).** The
   owner chose option B for Q3. The new action `job:reconcile` is checked by
   the job service. The reconcile principal holds no start action and no
   `job:cancel`, so a leaked credential of it cannot start a plan, an apply or
   a destroy.

## Consequences

- Each namespace has five more grants. The grant list of a housekeeping
  principal shows them, and a revoke of one stops only that purpose on that
  namespace.
- The port `CreateNamespaceWithGrant` of `access.GrantStore` takes a list of
  grants. The conformance suite gets the new cases first.
- The actions `unit:redact` and `namespace:manage-agent-runs` are classified
  in `pkg/access/access.go`, because the housekeeping roles hold them.
- ADR 216 decision 7 is unchanged: the five roles do not depend on the
  setting of the reach.
- The changes `housekeeping-principals-install`,
  `housekeeping-principals-sweeps` and `housekeeping-principals` build these
  decisions.
