# 338. The namespace service principal holds a stored grant

Status: superseded by ADR 370 · accepted 2026-11-07. Decided by the project owner on 7 November 2026

Changed by:

- ADR 369 changes decision 3: the namespace service principal has no role in
  a user automation.
- ADR 370 supersedes: the namespace service principal is removed.

Changes:

- Changes ADR 267 decision 14: the permission of the namespace service principal is a stored grant. It is not a rule in code.

## Context

ADR 267 decision 14 says that a start that no person makes runs as the namespace service principal. It also says that this principal holds only the action that starts a plan. The glossary says that it holds `unit:run-plan` on the units of its own namespace. The glossary also says that no grant changes what it holds. The code defines this permission.

CLAUDE.md says: every permission is a stored grant, with a holder, a role, an object, a creator and a time. A user can list, audit and revoke it. This includes the grants of the principals that the server makes. Code can define roles and actions. Code must not define who holds them.

On 7 November 2026 the project owner decided this point.

## Decision

Decided by the project owner on 7 November 2026.

1. **The permission is a stored grant (owner, 2026-11-07).** The server makes a stored grant in the same transaction that makes the namespace. The grant is for the namespace service principal. The grant gives the principal the action `unit:run-plan` on its own namespace. The creator of the grant is the creator of the namespace. The grant has the time of the namespace creation.
2. **A person can list and revoke the grant (owner, 2026-11-07).** The grant shows in the list of grants like each other grant. A holder of the right to manage access can revoke it. The decision that a trigger starts a plan then names the grant that allowed it.
3. **The principal still holds nothing else (owner, 2026-11-07).** The grant covers the start of a plan only. The principal still has no token and no session. The rule of ADR 267 decision 14 about the read check of the last saver does not change.

## Consequences

- A revoked grant makes a trigger start fail closed. The server writes the denial to the audit log, as for each other denial.
- Code no longer defines who holds `unit:run-plan` for this principal. The code defines only the action and the role.
- The definition of "Namespace service principal" in `GLOSSARY.md` says that no grant changes what the principal holds. That sentence is no longer true. The code change updates the glossary.
- This ADR does not decide a one-step restore of a revoked grant. The remedy is a new grant.
- A namespace that exists before this change needs the grant. This ADR does not decide how. No tag shipped the old shape, so the rule of ADR 322 applies: no migration code.
