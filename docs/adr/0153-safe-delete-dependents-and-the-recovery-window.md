# 153. Safe delete: dependents and the recovery window

Status: accepted · 2026-10-17

## Context

The project owner asked for a safe delete of a unit or a resource: a check
of what depends on it, and a recovery window.

What exists:

- **Soft delete (ADR 47).** A delete through a tool is a soft delete. The
  name is free at once. The server keeps the state, the history, the data key
  and the grants until the purge. The retention period is 30 days by
  default (`--soft-delete-retention`). `unit:restore` brings a unit back, and
  `unit:purge` ends it before the period ends.
- **The Pulumi delete guard.** `pulumi stack rm` without `--force` sends
  `DELETE /api/stacks/{namespace}/{project}/{stack}`. The server answers 400
  when the stack still holds resources, and the Pulumi CLI then tells the
  user to use `--force`. With `--force`, the CLI sends `?force=true`, and the
  server deletes.
- **The OpenTofu delete.** `tofu workspace delete` through the cloud backend
  reads the workspace. When the workspace permissions hold
  `can-force-delete`, and the user did not give `-force`, OpenTofu calls the
  TFE safe-delete action:
  `POST /api/v2/organizations/{organization}/workspaces/{workspace}/actions/safe-delete`
  (`internal/cloud/state.go` at commit `52ade2f`, `State.Delete`). Without
  that permission, or with `-force`, it sends `DELETE`. The server serves
  only `DELETE` today, and it does not answer `can-force-delete`. So every
  `tofu workspace delete` is a forced delete now.
- **What the server sees of dependencies.** A Pulumi stack reference reads
  the outputs of another stack through the Pulumi Cloud-compatible API. An
  OpenTofu `terraform_remote_state` data source with the `remote` or `cloud`
  backend reads the current state version or its outputs through the
  TFE-compatible API. In both cases, the server sees which principal reads
  which unit. It does not see which unit the reader works on. A principal
  that runs `pulumi up` on stack A holds the lease of an update on A. A
  principal that runs `tofu apply` on workspace A holds the lock of A.
- The server never deletes a resource and never edits state (ADR 28
  decision 3). The tool deletes resources, for example with `pulumi destroy`,
  `pulumi state delete`, `tofu destroy` or `tofu state rm`. The server then
  receives a new state item.

## Decision

### Dependents of a unit

1. **A dependency edge.** An edge says that a dependent reads the outputs of
   a source unit. It holds the source unit (tool, namespace, unit ID), the
   dependent unit when the server knows it, the principal subject, the kind,
   the evidence, the first and the last time seen, and a count of reads. The
   `dependency` package holds the edges. It is tool-neutral. An edge can link
   units of two tools, because both are units (ADR 13).
2. **Kinds.** The first kind is `output-read`. A later feature that declares
   a link between units adds its own kind, for example run triggers (RFC 24)
   or output grants (RFC 15). Such an edge has the evidence `declared`.
3. **Evidence.** The server records how it knows the dependent:
   - `job`: the access token of a job made the read, and the job names its
     unit (RFC 6). This is exact.
   - `lock`: at the time of the read, the reading principal held a Pulumi
     update lease or an OpenTofu workspace lock on another unit. The server
     records one edge for each unit that the principal held. When it held
     more than one, each edge is `ambiguous`.
   - `principal`: the principal held nothing. The edge names the principal
     only, for example a CI account.
   - `declared`: decision 2.
4. **Recording.** Each tool package records an edge when it serves an output
   read. For Pulumi, this is a read of the outputs or of the export of a
   stack. For OpenTofu, this is a read of the current state version, its
   download, or its outputs. The write is a fenced write in the namespace of
   the source. It is a side effect of a read, so:
   - The server updates an edge at most one time for each hour
     (`dependency.RecordInterval`), with the last time seen and the count.
   - A failed edge write never fails the read. The server counts it in a
     metric and writes a log line.
   - An instance that is not in the home region of the source namespace
     (ADR 4) records no edge.
   - A read by a principal that holds the lease or the lock of the source
     unit itself records no edge. The tool then reads its own state.
5. **Which edges block a delete.** An edge with the evidence `job`, `lock`
   or `declared`, last seen within the dependents window, blocks a delete
   that is not forced. The window is 90 days by default, with the setting
   `--dependents-window` (`DUSTPAN_DEPENDENTS_WINDOW`). An edge with the
   evidence `principal` never blocks, but the check lists it.
   `dependency.Blocking` defines this rule.
6. **The edges after a delete.** A soft delete keeps the edges of the unit,
   so a restore brings them back. A purge deletes every edge where the unit is
   the source or the dependent.

### The delete check

7. **A check before the delete.** The inventory API answers the dependents
   of a unit:
   `GET /api/inventory/units/{tool}/{unit_id}/dependents`. The answer lists
   each edge with its kind, its evidence, its times and the dependent, and
   says which edges block a delete. It also answers the resource count of
   the latest state item. It needs `unit:read` on the unit. It names only
   the dependent units that the caller can read (`unit:read`), and it counts
   the others (ADR 17). A principal-only edge names the principal subject.
8. **Pulumi.** `DELETE` without `force` keeps the delete guard for resources
   (400). When the stack holds no resources and a blocking edge exists, the
   answer is 409 as `{"code": 409, "message": "..."}`. The message names the
   count of dependent units, names up to three that the caller can read, and
   tells the user to use `pulumi stack rm --force`. The status differs from
   400, so the CLI shows the message and does not report resources. With
   `force=true`, the server deletes as it does now.
9. **OpenTofu.** The workspace resource answers the permission
   `can-force-delete`, true when the caller has `unit:manage`. OpenTofu then
   calls safe-delete when the user gives no `-force`. The TFE-compatible API
   serves:
   - `POST /api/v2/organizations/{organization}/workspaces/{workspace}/actions/safe-delete`,
   - `POST /api/v2/workspaces/{workspace_id}/actions/safe-delete`.

   Safe-delete answers 409 as a JSON:API error when the workspace holds
   resources, when it is locked, or when a blocking edge exists. The detail
   names the cause, and for dependents it names the count and up to three
   names. Otherwise it is the soft delete of `DELETE`. `DELETE` stays a
   forced delete. The e2e step shows the text that OpenTofu prints for the
   409 on each supported version.
10. **The audit entry.** The audit entry of each delete holds the count of
    blocking edges at the time of the delete, as a change with the key
    `dependents`. So a reader of the audit log sees that a forced delete went
    past dependents.
11. **Purge.** A purge before the end of the retention period
    (`unit:purge`) runs the same check. With a blocking edge, it needs the
    request field `confirmDependents: true`, else it answers 409. The
    retention sweep purges without the check, because the retention period is
    the recovery window that the team had.

### The recovery window

12. **The recovery window of a unit is its retention period.** The recovery
    window is the time from the delete to the purge: the retention period of
    ADR 47. In this window, `unit:restore` brings the unit back with its ID,
    state, history, secrets, grants and edges. This ADR adds no second
    period.
13. **Reads of a deleted unit.** In the recovery window, a read of the
    outputs of the deleted unit through its old name answers 404, as now. When
    no live unit holds the name, the server counts the read on the deleted
    unit, with the time and the principal. The deleted units list of the
    inventory API answers this count and the last time. So a person sees that
    something still reads a deleted unit, and can restore it.

### Resources

14. **The server does not delete resources.** The tool deletes a resource.
    The server keeps the record. A safe delete of a resource is a check
    before the tool runs, and a record after it.
15. **Dependents of a resource in its unit.** A masked read answers the
    resources of the same state item that depend on one resource:
    - Pulumi: the resources whose `dependencies`, `propertyDependencies`,
      `parent` or `deletedWith` name its URN.
    - OpenTofu: the resource instances whose `dependencies` name its
      resource address.

    Routes, as proposals that the e2e step sets:
    `GET /api/stacks/{namespace}/{project}/{stack}/state/{version}/resources/{urn}/dependents`
    and `GET /api/v2/state-versions/{state_version_id}/resources/{address}/dependents`.
    Both need `unit:read`. The answer also lists the dependent units of the
    whole unit (decision 7), because the server cannot map an output to a
    resource.
16. **The record after a delete.** Blame (ADR 150) records each resource that
    a state item deletes, with the actor and the job. The previous state item
    still holds the record of the resource. A restore of that item (ADR 28)
    brings back the record, never the infrastructure. A plan of a job
    (ADR 152) that deletes resources can show the dependents of decision 15 to
    the policy check (RFC 13). RFC 13 decides how.

### Actions

17. The actions of this ADR, with the object of each:

    | Operation | Action | Object |
    | --- | --- | --- |
    | Read the dependents of a unit | `unit:read` | unit, and `unit:read` on each dependent that the answer names |
    | Read the dependents of a resource | `unit:read` | unit |
    | Delete, safe-delete, forced delete | `unit:manage` | unit |
    | Restore in the recovery window | `unit:restore` | unit |
    | Purge, with or without `confirmDependents` | `unit:purge` | unit |

    These actions exist. This ADR adds no action (question 1).

## Relation to the RFCs

- **RFC 17 and ADR 47:** the recovery window is the retention period of
  ADR 47, and a delete frees the name at once, as the follow-up in RFC 17
  says. This ADR adds the dependents check to the delete, the safe-delete
  routes and the purge confirmation.
- **RFC 66** lists "References between units" as an item that an export and
  an import can break. The dependents route of decision 7 answers that item
  for a unit, so the check mode of RFC 66 can read it.
- **RFC 15 and RFC 24** add declared edges when they land (decision 2).
- **RFC 18 and ADR 28:** the record of a deleted resource comes back through
  the restore of ADR 28 (decision 16).

This ADR supersedes no decision. It adds to ADR 47, ADR 25 and ADR 28.

## Consequences

- `tofu workspace delete` without `-force` becomes a safe delete. A user
  who deletes a workspace that holds resources now gets an error, as with
  TFE. The release notes say this.
- Each output read can write one row for each hour and edge. On a namespace
  with many readers, this is a write load on the read path. The release load
  run (ADR 40) measures it.
- The `lock` evidence is an inference. A principal that holds a lock on one
  unit and reads another unit for another reason makes a false edge. The
  check says that such an edge is inferred, and a forced delete still works.
- An edge from a workstation that holds no lock names only the principal. A
  team that wants exact edges runs its tools in jobs (RFC 6).
- Open work. Each route lands with its e2e step first (ADR 12) and its action
  in the route table (ADR 17):
  1. The `dependency` ports and their conformance suite
     (`dependencytest.Run`), then the SQL adapters and migrations.
  2. Edge recording in the output read paths of both tools.
  3. The dependents route of the inventory API.
  4. The Pulumi 409 for dependents, with its e2e step.
  5. `can-force-delete`, the two safe-delete routes, and the e2e step on each
     OpenTofu version.
  6. `confirmDependents` on the purge.
  7. The read count of a deleted unit.
  8. The routes for the dependents of a resource.

## Open questions

1. **A separate action for a forced delete.** A forced delete goes past
   resources and dependents with `unit:manage`, as the CLIs expect. Should a
   role be able to give a delete without the forced form, for example with
   `unit:delete-forced`? The Pulumi CLI sends both forms to the same route.
2. **Dependents in other namespaces.** A reader in another namespace can read
   the outputs of a unit. The edge is in the namespace of the source. The
   check names the dependent only when the caller can read it. Is a count
   enough for dependents that the caller cannot see?
3. **Edges from the `lock` evidence.** Should the check let a person mark an
   inferred edge as wrong, so that it stops blocking?
4. **The dependents window.** Is 90 days the right default?
