# 47. Soft delete, and a unit never moves between namespaces

Status: accepted · 2026-10-12 · the final delete superseded in part by ADR 151

## Context

[RFC 17](../rfc/0017-unit-lifecycle.md) found that HCP Terraform cannot undo a
workspace delete. ADR 19 and ADR 23 leave to RFC 17 what a deleted stack
keeps and when the final delete happens. ADR 19 also leaves open a move of a
stack to another namespace, because `secret.Scope` includes the namespace. On
2026-10-12 the project owner answered both points.

The owner said that retention is "Configurable, default to 30 days or
something reasonable". The owner said that "a user with permission by our FGA
model" can restore a unit. The owner said about a move between namespaces:
"No. Namespaces can obviously be moved between instances for failover or DR,
but they own their contents."

## Decision

1. **A delete is a soft delete.** The server keeps the state of a deleted unit
   for a retention period. During this period a person can restore the unit.
2. **Retention is configurable.** The default is 30 days.
3. **The final delete happens when the retention period ends.** This is the
   final delete of ADR 23, decision 5. The server keeps the data key of a
   stack until then, so that a restore can read the secrets again.
4. **The authorization model decides who can restore.** A restore is an
   action on the unit reference (ADR 13, ADR 17). A principal that the model
   allows can restore. There is no special role. The endpoint chooses the name
   of the action.
5. **A unit never moves between namespaces.** The server has no action and no
   endpoint for it. A unit belongs to its namespace.
6. **A namespace moves between installations.** For failover or disaster
   recovery (ADR 4), a namespace moves with its contents.
7. **Export and import are the way out.** A person exports a unit and imports
   it into any namespace, on any installation (ADR 29).

## Not decided here

- What else a deleted unit keeps, besides state and the data key. Examples:
  history and the inventory row.
- How a restore treats a unit that has the same name after the delete.
- Whether an administrator can delete a unit before the retention period ends.
- What an import carries over. The owner asks: what happens to the access
  grants and to the bound profiles in the target namespace? A new RFC covers
  tooling with checks for what an export and an import cannot carry over.
- The mapping of each CLI command to an endpoint, destroy on delete, and tags
  as labels (RFC 4). They stay first ideas in RFC 17.

## Consequences

- ADR 19 lists the move of a stack as a follow-up. This ADR answers it: no
  move exists.
- The Pulumi CLI has a stack transfer between organizations. The server does
  not serve it. Check the answer to this call with `make e2e` before an
  endpoint ships.
- A backup holds soft-deleted units until the final delete (ADR 30).
