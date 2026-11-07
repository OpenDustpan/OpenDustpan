# 346. A unit moves between collections, never between namespaces

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 47 decisions 5 and 7: no one-click move between namespaces, and export and import stay the way out.
- Extends ADR 51 decision 6: a move between collections lists what breaks.

## Context

ADR 47 decision 5 says that a unit never moves between namespaces. ADR 51 decision 6 lets a person move a unit to another collection with an API call. A grant can name a namespace, a collection or a unit (ADR 51 decision 8). A move can change who has access to the unit.

On 7 November 2026 the project owner decided how a move works for the person who asks for it.

## Decision

Decided by the project owner on 7 November 2026.

1. **No one-click move between namespaces (owner, 2026-11-07).** The server has no one-click move of a unit to another namespace. Such a move would break grants. A person exports the unit and imports it into the other namespace, as ADR 47 decision 7 says.
2. **A person can move between collections (owner, 2026-11-07).** A person can move a unit to another collection of the same namespace. The authorization model must allow it (ADR 51 decision 6).
3. **The server lists what breaks, and the person confirms (owner, 2026-11-07).** Before the move, the server lists the exact effects of the move. The person confirms the list before the move happens. Each item names the principal or the rule, and the reason. Example: "Alice will lose access because she is not a member of the target collection".
4. **The list is in the API.** The move endpoint returns the list. The console, the CLI and other clients show the same text (ADR 17).

## Consequences

- This ADR does not decide if the server also refuses a move that breaks a grant.
- This ADR does not decide which effects the list covers beyond grants, for example bound profiles, output-sharing rules and saved views.
- The user documentation for collections and for export and import needs a page on the move and on the list.
