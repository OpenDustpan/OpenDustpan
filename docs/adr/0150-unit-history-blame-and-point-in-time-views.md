# 150. Unit history: blame and point-in-time views

Status: accepted · 2026-10-17 · supersedes part of ADR 13 (see "What this ADR replaces"). Decisions 14, 15 and 16 superseded in part by ADR 154. ADR 199 changes decision 1

## Context

The project owner asked for four features with one design, for both tools:

1. **Blame.** Which update or state version changed a resource, which
   principal made it, and from which job.
2. **Redaction** of old history (ADR 151).
3. **Point-in-time views.** The state of a unit at a time, and the inventory
   of a namespace at a time.
4. **Safe delete** of a unit, with a check of what depends on it and a
   recovery window (ADR 153).

Stored plans and job logs are history too. The job model (ADR 102) stores
them. Redaction (ADR 151) and the retention policy (ADR 152) apply to them.

This ADR covers blame and the point-in-time views, and the parts that all
four features share. The facts that it builds on:

- ADR 13 keeps the tool models separate. A Pulumi stack has updates, and each
  mutating update has a checkpoint (ADR 24, ADR 37). An OpenTofu workspace
  has state versions with a lineage and a serial (ADR 26). Cross-tool views
  read the inventory projection. Each tool publishes to it in the same
  transaction as its own write.
- ADR 41 lets the server read state with secret values masked. The masked
  state diff of each tool already compares two versions: the Pulumi diff
  keys resources by URN, and the OpenTofu diff keys resource instances by
  address.
- ADR 28 gives each tool a history list and a restore. The Pulumi update
  records `CreatedBy` and the ID of the access token (`updates.token_id`).
  The OpenTofu state version records the lock holder, and no token ID.
  Neither records a job.
- RFC 6 proposes runners and jobs. No job exists yet. A job runs the tool on
  a runner, and the tool talks to the server as usual.
- ADR 72 runs background work as durable operations, with a claim and a
  cursor in the database.

The owner approved every decision of this ADR on 2026-10-17, after review
round 1 of the pull request that adds it.

## Decision

### Shared terms

1. **History item.** A history item is one entry in the history of a unit
   that has content: one Pulumi update with its checkpoints, events and
   journal entries, one OpenTofu state version, one stored plan, or one
   version of a job log. The job model (ADR 102) owns plans and job logs.
   Each tool keeps its own items in its own tables (ADR 13). Shared code names an item
   by the unit (tool, namespace, unit ID), a kind (`state`, `plan` or
   `job-log`) and the ID that the tool gives the item. The core never parses
   the item ID.
2. **The latest state item.** For a stack, it is the update that holds the
   checkpoint that `stack export` answers. For a workspace, it is the current
   state version. ADR 151 decision 12 adds the replay base of each update
   that has not ended or whose journal is not replayed yet. Redaction never
   touches the latest state item.
3. **Actor.** Each new history item records an actor: the principal subject,
   the access token ID, and the job ID. The token ID is empty for a bootstrap
   token. The job ID is empty when no job made the item. The update row
   already keeps the token ID in `updates.token_id`. The state version gets
   the token ID and the job ID, and the update gets the job ID, as new
   columns from the start, as ADR 41 decision 13 does for audit entries. A
   job token carries the job-ID claim that ADR 100 defines, so a write that a
   job token makes records the job ID. The `history.Actor` type holds the
   three fields.

### Blame

4. **Each tool computes blame for its own items.** After a new state item
   commits, a durable operation (ADR 72) compares it with the previous state
   item of the same unit. It writes one resource change row for each resource
   that the new item creates, changes, replaces or deletes. The rows are in
   the tables of the tool: `stack_resource_changes` and
   `workspace_resource_changes`. No table is shared.
5. **Resource identity.**
   - Pulumi: the URN. A resource with the same URN and a new `id` is
     `replaced`.
   - OpenTofu: the resource instance address, for example
     `module.net.aws_subnet.a[0]`. An instance with the same address and a new
     `id` attribute is `replaced`.
6. **Change kinds.** `created`, `updated`, `replaced` and `deleted`. A row
   also holds the count of value paths that changed, never a value and never a
   value path of a secret value. The comparison uses the masked diff of each
   tool (ADR 41). A change to a secret value counts as a change of its
   resource:
   - Pulumi: a secret value is ciphertext under the stack data key, and the
     CLI can encrypt the same plaintext to another ciphertext. So the blame
     operation decrypts each secret value inside the server, which holds the
     stack data key, and compares the SHA-256 digests of the plaintexts. It
     never stores, logs or answers a digest or a plaintext, and it writes no
     audit entry, because no person reads a value.
   - OpenTofu: the masked diff compares the values of sensitive paths inside
     the server, and answers only that they changed.
7. **Order of items.**
   - Pulumi: the update version. Only updates that saved a checkpoint are
     state items. A preview is not a state item.
   - OpenTofu: the order in which state versions finalize. A forced write and
     a restore (ADR 28) are state items like any other. A restore row records
     `restored-from`, so blame shows that a restore made the change.
   - A state version with another lineage than the state version before it
     compares with that state version, as any other. Its blame and each of
     its rows have the mark `lineage-changed`, so a reader sees that a forced
     write started a new lineage.
8. **The blame cursor.** Each unit has a blame cursor: the last state item
   that the operation compared. The operation compares items in order and
   moves the cursor in the transaction that writes the rows. A repeated run
   for the same item writes the same rows and changes nothing. The cursor
   rule of ADR 72 applies.
9. **Client-encrypted OpenTofu state.** The server cannot read such a state
   (ADR 41 decision 8). The operation writes no resource rows for it. It marks
   the item `opaque`, and the next readable item compares with the last
   readable item before it.
10. **Queries.**
    - Resource history: every change of one resource of one unit, newest
      first, with the item, the actor, the time and the change kind.
    - Item changes: every resource change that one item made.

    Both need `unit:read` on the unit. A resource change row is metadata.
    Redaction keeps it (ADR 151).
11. **Routes.** The routes stay in the tool adapters (ADR 28 decision 1).
    The paths below are proposals. The e2e step of each route sets the final
    path, and it shows that no CLI version rejects it.
    - Pulumi: `GET /api/stacks/{namespace}/{project}/{stack}/resources/history?urn=...`
      and `GET /api/stacks/{namespace}/{project}/{stack}/updates/{version}/resource-changes`.
    - TFE-compatible API: `GET /api/v2/workspaces/{workspace_id}/resource-history?filter[address]=...`
      and `GET /api/v2/state-versions/{state_version_id}/resource-changes`, as
      JSON:API documents.

### Point-in-time views

12. **A unit at a time.** A request names a unit and a time `at` (Unix
    milliseconds). Each tool finds the state item that was current at that
    time:
    - Pulumi: the update with the highest version that saved a checkpoint and
      ended at or before `at`. The view answers per update, not per
      checkpoint save. When an update ran at `at`, the response also names
      it, because its checkpoints were not final at that time.
    - OpenTofu: the state version with the latest finalize time at or before
      `at`.

    The response is the masked state read of that item (ADR 41), with the
    item and its actor. A time before the first state item answers 404 with
    an error that names the time of the first item. The view needs
    `unit:read`.
13. **A redacted item at a time.** When the item is redacted (ADR 151), the
    response says so and holds no state. It lists the resources that the unit
    held at that time, from the resource change rows (decision 10). Those rows
    are metadata, so this list survives redaction.
14. **Access to a view at a time.** Every view at a time uses the access of
    the caller now, never the access of the caller at the time `at`:
    - A caller who lost `unit:read` on a unit cannot read a view of the unit
      at any time, also a time when the caller had access.
    - A caller who got `unit:read` today can read a view of the unit at any
      time in its history, also before the grant.
    - A renamed unit: the request names the unit by its current name or by
      its ID. The response holds the name of the unit at the time `at`, from
      the inventory change log, and its current name.
    - A soft-deleted unit (ADR 47): the tool routes answer 404 for its old
      name, as now. The inventory view at a time lists the unit when it
      existed at `at` and the caller has `unit:read` on it now. Grants stay
      until the purge (ADR 47), so this check works. The entry has the mark
      `deleted` with the time of the delete.
15. **Routes for the unit view.** Each tool adapter adds the parameter
    `at` to the masked state read routes that exist. Pulumi:
    `GET /api/stacks/{namespace}/{project}/{stack}/state/at/{at}/resources`.
    TFE-compatible API: `GET /api/v2/workspaces/{workspace_id}/state-at/{at}`.
    The e2e step sets the final path.
16. **The inventory at a time.** The inventory gets an append-only change
    log. Each tool appends one row in the same transaction as each inventory
    write that it makes now: `created`, `changed`, `renamed`, `deleted`,
    `restored` and `purged`. A row holds the fields of the inventory entry at
    that time, the time of the write, and a sequence number. The store assigns
    the sequence number in the transaction of the write. It increases in the
    order of commits, so two instances with clocks that differ cannot change
    the order of two rows. The inventory at a time `at` is, for each unit, the
    row with the highest sequence number among the rows at or before `at`. A
    unit whose row is `deleted` or `purged` is not in the answer, except
    as decision 14 says for a soft-deleted unit. `inventory.ReplayAt` defines
    the answer, so a store that holds its rows in memory can use it.
17. **Route for the inventory view.** `GET /api/inventory/units?namespace=...&at=...`
    adds the parameter `at` to the inventory list. The list keeps its filter
    rule: it answers only the units that the caller can read now, and it says
    when it left out units (ADR 17). A purge ends every grant on a unit
    (ADR 47), so the view leaves out a purged unit and counts it as left out.
    The view never names a purged unit, also to a namespace administrator.
18. **The change log keeps names, for ever.** A purge (ADR 47) keeps the
    change log rows of the unit. They hold names and counts, never state, in
    the style of audit entries (ADR 60 decision 6). A row holds no foreign key
    to the unit. The server keeps the change log for ever, as the audit log
    (ADR 68). No setting changes this.

### Actions

19. The actions of this ADR, with the object of each:

    | Operation | Action | Object |
    | --- | --- | --- |
    | Resource history, item changes | `unit:read` | unit |
    | A unit at a time | `unit:read` | unit |
    | The inventory at a time | `namespace:read`, then `unit:read` for each unit | namespace, unit |

    These actions exist. This ADR adds no action.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it replaces:

- **ADR 13, "Inventory":** "it can be rebuilt from the tool packages'
  data". The live inventory still can. The change log of decision 16 cannot
  be rebuilt in full, because the tool tables do not keep old names.

## Relation to the RFCs

- **RFC 18** keeps retention and the manual lock open. This ADR adds no
  retention for state items. Blame builds on the history routes of ADR 28.
- **RFC 39** proposed an indexer with a fixed list of fields and never
  attribute values. A resource change row follows that rule: it keeps the
  resource identity, the change kind and a count, never a value.
- **RFC 6** proposes jobs. ADR 100 defines the job model and the job-ID
  claim of a job token, which the actor reads.

## Consequences

- Each mutating update and each state version starts one durable operation.
  The operation reads two state items and writes rows. The release load run
  (ADR 40) measures its cost on the largest checkpoints that it uses.
- Blame lags the write by the time of the operation. The history response
  says when blame for an item is not ready yet.
- The inventory change log grows by one row for each inventory write, and
  the server keeps it for ever. The sizing guide states the growth.
- ADR 13 says that the inventory "can be rebuilt from the tool packages'
  data". The change log cannot be rebuilt in full, because the tool tables do
  not keep old names after a rename. A rebuild makes the current entries and
  one `created` row for each unit. The documents say this.
- A restore (ADR 28) is a state item. So blame shows a restore as the change
  that brought back a resource, with the actor who restored it.
- Open work, in this order. Each route lands with its e2e step first
  (ADR 12) and its action in the route table (ADR 17):
  1. The actor columns: the job ID on updates, and the token ID and the job
     ID on state versions.
  2. The resource change ports and their conformance suites
     (`stackhistorytest`, `workspacehistorytest`), then the SQL adapters and
     migrations.
  3. The blame operation of each tool, with its cursor.
  4. The point-in-time port of each tool. Its conformance cases need the
     update and state version tables, so they land in `storetest` with the
     adapters.
  5. The inventory change log, its port, the sequence number and the `at`
     parameter.
  6. The routes of decisions 11, 15 and 17, with the access rules of
     decision 14.

## Answered questions

The owner approved these answers on 2026-10-17. The decisions above apply
them.

1. **Granularity of a Pulumi view at a time.** Per update (decision 12).
   Blame is per update too.
2. **Blame for the first item of a lineage.** Blame compares across
   lineages, and marks the item `lineage-changed` (decision 7).
3. **Retention of the inventory change log.** The server keeps it for ever
   (decision 18).
4. **Job ID from a job token.** ADR 100 defines the job-ID claim of a job
   token (decision 3).
5. **Purged units in the inventory at a time.** The view counts purged
   units as left out and never names them (decision 17).
