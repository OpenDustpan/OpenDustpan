# 13. Separate tool models; shared access control and inventory

Status: accepted · 2026-10-10 · the rebuild of the inventory superseded in part by ADR 150 · extended by ADR 247 (labels and `consoleUrl` on the inventory entry)

Changed by:

- ADR 19 extends: the unit ID in its own inventory column.
- ADR 41 changes: the words opaque state.
- ADR 150 changes: the rebuild of the inventory.
- ADR 247 extends: labels and consoleUrl on the inventory entry.
- ADR 265 extends: a set of unit states replaces the one status.

## Context

ADR 12 makes OpenDustpan a backend for several infrastructure-as-code tools.
Their concepts look alike but are not the same, and the obvious mistake is to
unify them into one model. Compared for the Pulumi CLI and OpenTofu (via the
TFE-compatible API):

| Pulumi CLI | OpenTofu | Same thing? |
| --- | --- | --- |
| Organization | Organization | Yes: both are a namespace (ADR 10). |
| Project | Project | No. A Pulumi project is a program; stack names are unique within it. A TFE project only groups workspaces, whose names are unique per organization. |
| Stack | Workspace | Close, not equal. Both are one deployable instance with its own state. A stack carries config, secrets and an update history with events; a workspace carries an explicit lock and state versions with lineage and serial. |
| Update | Run | No. The Pulumi CLI reports every update with a lease and events. OpenTofu with local execution creates no run: it locks, uploads a state version and unlocks. |
| Update lease (expires) | Workspace lock (explicit) | No. Different behavior on crashes and forced unlocks. |
| Checkpoint | State file | Only as storage. Both are opaque to the server and can share the blob port, never a format. |
| Component | Module | Not a backend concept. Both are groupings inside the program; the server sees them only as parent links (Pulumi) or address prefixes (OpenTofu) inside opaque state. |

A model forced over both ends up fitting neither protocol, and each mismatch
becomes a compatibility bug against a real CLI (naming uniqueness alone
differs).

Users still want one experience across tools: one set of permissions, one list
of what is deployed, one audit trail.

## Decision

Separate write models:

- Each tool keeps its own domain model, state machine and storage schema
  (`stack` for the Pulumi CLI, `workspace` for OpenTofu), as ADR 12 requires.
  No type, table or port is shared between tool packages.
- Components and modules stay inside opaque state. The server never models
  them. Grouping resources for display is a console concern, done per tool.

Shared, tool-neutral core (already shared or planned): namespaces and fencing,
identity, secrets encryption, blob storage, and the audit trail.

A tool-neutral name for a tool's deployable unit:

- In shared code, a Pulumi stack or an OpenTofu workspace is a **unit**. A unit
  is referenced as tool + namespace + the tool's own name for it
  (`pulumi:acme/web/prod`, `opentofu:acme/network`). Each tool package maps its
  units to this reference; the core never parses the tool-specific part.
- The UI and API responses keep each tool's own words (stack, workspace). Only
  shared code and cross-tool views say "unit".

Access control:

- Authorization decisions are made on tool-neutral references and tool-neutral
  actions, so one rule can cover a team's stacks and workspaces alike. Today the
  `Authorizer` takes a namespace; when it becomes finer-grained it takes a unit
  reference, defined in the core.
- Actions are tool-neutral verbs: read state, write state (apply), manage
  (create, rename, delete, unlock), administer the namespace. A tool may add
  its own actions only for things no other tool has.

Inventory (a cross-tool read model):

- A core read model lists every unit in a namespace with tool, reference,
  display name, last change time, status and resource count where known. The
  console's cross-tool views, search and activity feeds read from it.
- It is a projection: each tool package publishes changes to it through a core
  port, in the same transaction as the tool's own write, so it replicates with
  the namespace like any other data (ADR 4). Nothing else writes to it, and it
  can be rebuilt from the tool packages' data. Tool-specific views read the tool's own model.

## Consequences

- The `identity` actions `stack:read` and `stack:write` are Pulumi words in a
  shared package. Rename them to tool-neutral actions before roles, teams or
  the TFE-compatible API's authorization exist, while nothing depends on them.
- The inventory port, the unit reference type and their conformance tests come
  before the console shows anything across tools. A console that queries each
  tool's API and merges results in the browser is a stopgap only.
- Adding a tool means mapping its units to references, publishing to the
  inventory and mapping its operations to the neutral actions. Its ADR (ADR 12)
  must include that mapping.
- Per-unit authorization costs a check per unit when listing. That is fine
  until memberships exist (ADR 10) and filtering moves into queries.
