# 384. The roles that hold unit:redact, and the action that a rejection needs

Status: accepted · 2026-11-15. Decided by the project owner (owner, 2026-11-15)

Changes:

- Extends ADR 151 decision 29: "No other built-in role gets it" means no other built-in role of a namespace or of a unit. The roles `account-administrator` and `site-administrator` hold `unit:redact` through the family `data` and the family list of ADR 216 decision 17.
- Changes ADR 103 decision 8: a rejection by a person other than the requester of the job needs `job:cancel` in addition to `job:approve`.

## Context

Two OpenSpec changes stopped on a question that no ADR answered. The project owner answered both on 15 November 2026.

- `job-artifact-store-redaction` puts `unit:redact` in a family so that the stored grant of the built-in role `admin` counts. ADR 151 decision 29 says: "The built-in roles of a namespace administrator and of a unit administrator get it (ADR 52). No other built-in role gets it." The family `data` also gives the action to `account-administrator` and `site-administrator`, because the code builds their built-in sets from the families (`pkg/access/roles.go`). The words of the two ADRs allowed two readings.
- A rejection stops a job as a cancel does. Today a rejection needs `job:approve` only (`pkg/job/service_approve.go`), so a person without `job:cancel` can stop the job of another person.

## Decision

1. **`unit:redact` is in the family `data` (owner, 2026-11-15).** The owner chose the ADR 216 reading. The action goes in an existing family, `data`, as `unit:purge` does. The built-in roles `admin`, `account-administrator` and `site-administrator` hold it. No built-in role of a namespace or of a unit other than `admin` holds it: `viewer`, `writer`, `maintainer` and `planner` do not. ADR 216 is not amended. The authorizer pass of the `Admin` flag does not change.
2. **A rejection needs the cancel action (owner, 2026-11-15).** A person other than `Job.StartedBy` that gives the verdict `reject` on an approval gate needs `job:cancel` on the unit. The person also needs `job:approve`. The requester needs `job:approve` only. The job service and the HTTP handler each check it, and a refusal writes an audit entry. The confirmation of a job, and its rejection by the person who asked, keep the start action (ADR 103 decision 9).
3. **The wish and the open question.** The owner wishes that a role that can start a job can also cancel it. Only `planner` fails this: it does not hold `job:cancel`. An answer for `planner` changes ADR 212 decision 1 and ADR 373 decision 1. It needs an RFC first. This ADR does not decide it.

## Consequences

- The roles that hold `unit:redact` follow the family `data`. A role that must not hold it needs a set without that family.
- The approvals route has one more check. The docs page of approval rules says which action a rejection needs.
- The OpenSpec changes `job-artifact-store-redaction` and `job-reject-needs-cancel` implement the two decisions.
