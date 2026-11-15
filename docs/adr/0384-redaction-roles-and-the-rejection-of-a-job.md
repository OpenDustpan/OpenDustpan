# 384. The roles that hold unit:redact, and the action that a rejection needs

Status: accepted · 2026-11-15. Decided by the project owner (owner, 2026-11-15)

Changes:

- Extends ADR 151 decision 29: the roles that hold `unit:redact` are the built-in role `admin` only. The account administrator and the site administrator do not hold it.
- Extends ADR 216 decision 17: the sentence "`site-administrator` holds all families" has one exception, the family `redaction`.
- Changes ADR 103 decision 8: a rejection by a person other than the requester of the job needs `job:cancel` in addition to `job:approve`.

## Context

Two OpenSpec changes stopped on a question that no ADR answered. The project owner answered both on 15 November 2026.

- `job-artifact-store-redaction` puts `unit:redact` in a family so that the stored grant of the built-in role `admin` counts. ADR 151 decision 29 says: "The built-in roles of a namespace administrator and of a unit administrator get it (ADR 52). No other built-in role gets it." The family `data` would also give the action to `account-administrator` and `site-administrator`. The code builds the built-in sets of those two roles from that family (`pkg/access/roles.go`).
- A rejection stops a job as a cancel does. Today a rejection needs `job:approve` only (`pkg/job/service_approve.go`), so a person without `job:cancel` can stop the job of another person.

## Decision

1. **Only `admin` holds `unit:redact` (owner, 2026-11-15).** ADR 151 decision 29 stays as written. The action is in its own family, `redaction`. The built-in role `admin` holds that family. No other built-in role holds it, and that includes `account-administrator` and `site-administrator`. A person that needs it gets it through a custom role or a grant of `admin`. The authorizer pass of the `Admin` flag does not change.
2. **A rejection needs the cancel action (owner, 2026-11-15).** A person other than `Job.StartedBy` that gives the verdict `reject` on an approval gate needs `job:cancel` on the unit. The person also needs `job:approve`. The requester needs `job:approve` only. The job service and the HTTP handler each check it, and a refusal writes an audit entry. The confirmation of a job, and its rejection by the person who asked, keep the start action (ADR 103 decision 9).
3. **The wish and the open question.** The owner wishes that a role that can start a job can also cancel it. Only `planner` fails this: it does not hold `job:cancel`. An answer for `planner` changes ADR 212 decision 1 and ADR 373 decision 1. It needs an RFC first. This ADR does not decide it.

## Consequences

- The built-in sets of `account-administrator` and `site-administrator` name their families. A new action in the family `data` does not reach `redaction`.
- The approvals route has one more check. The docs page of approval rules says which action a rejection needs.
- The OpenSpec changes `job-artifact-store-redaction` and `job-reject-needs-cancel` implement the two decisions.
