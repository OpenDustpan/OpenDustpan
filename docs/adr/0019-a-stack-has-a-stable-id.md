# 19. A stack has a stable ID

Status: accepted · 2026-10-10 · the home region amended by ADR 248 (primary installation)

## Context

Today the namespace, the Pulumi project and the stack name are the only identity
of a stack. The tables `updates`, `checkpoints` and `update_events` copy these
three values as their key. A rename must then change every row that refers to
the stack.

`secret.Scope` already has an `ID` field for a stable identifier. RFC 3 says
that a scope must be stable for the lifetime of its ciphertexts. It also says
that stable stack IDs are a prerequisite for shipping encryption. The e2e
suite already runs `pulumi stack rename`. Workspaces already have a stable ID,
`workspace.ID`, with the prefix `ws-`.

The ticket
[Decide the stable identity of a stack](https://github.com/OpenDustpan/OpenDustpan/issues/33)
holds the questions and a brief with a recommendation for each.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

- **A surrogate ID.** Each stack has a stable ID, stored in the table
  `stacks`. The tables `updates`, `checkpoints` and `update_events`, and the
  scope of the stack's secrets, refer to this ID. They do not copy the
  namespace, the Pulumi project and the stack name.
- **Format.** `ident.NewID("stk-")`: the prefix `stk-` and 20 random lowercase
  characters from `a` to `z` and `2` to `7`. This is the scheme of
  `workspace.ID`. The ID is opaque. It is not a secret, and it does not sort
  by time.
- **The API shows the ID. The routes do not use it.** The stack response of
  the Pulumi Cloud-compatible API gets a new field `id`. Routes stay
  `/api/stacks/{namespace}/{project}/{stack}`, because the Pulumi CLI
  addresses stacks by name. This change only adds a field.
- **A rename keeps the ID.** The history, the checkpoint and the secrets scope
  stay with the stack.
- **Delete, then create with the same name, gives a new ID.** The new stack
  does not get the secrets, the history or the references of the old stack.
- **One rule, separate types.** Each tool package keeps its own ID type
  (`stack` and `workspace`), as ADR 13 requires. They share only this rule:
  every unit has a stable, prefixed, opaque ID that `ident.NewID` makes. A new
  tool picks its own prefix.
- **The unit reference does not change.** It stays `<tool>:<namespace>/<name>`
  (ADR 13), without the ID. Each row of the inventory has the unit's ID in its
  own column, so that a consumer can follow a unit through a rename. The core
  stores the ID as an opaque string and never parses it. This adds to ADR 13
  and does not contradict it.
- **The home region makes the ID.** The write that creates a stack makes its
  ID, in the namespace's home region, and the ID replicates with the row
  (ADR 4).

## Consequences

- New numbered migrations, in both dialects, add the ID to `stacks`, fill it
  for existing rows, then make it `NOT NULL` and unique. The child tables move
  to the ID in steps: add and fill the column, switch the ports, then drop the
  copied columns. No applied migration changes.
- Secrets (RFC 3, part 2) can use the stack ID as the scope.
- A delete must delete or detach every row that refers to the old ID. Soft
  delete (RFC 17) decides what a deleted stack keeps.
- Open follow-ups, not decided here:
  - Moving a stack to another namespace. `secret.Scope` includes the
    namespace, so a move changes the scope. No move exists until RFC 17
    decides it.
  - Authorization by name or by ID (ADR 13, ADR 17).
