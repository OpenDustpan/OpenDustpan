# 28. State history, restore and unlock

Status: accepted · 2026-10-11. Decision 3 superseded in part by ADR 151. Route paths of decisions 3 and 5 superseded in part by ADR 210

## Context

[RFC 18](../rfc/0018-state-operations.md) asks for the first recovery tools
of a state backend: history, restore and unlock. A brief of 2026-10-11 checked
the RFC against the code and against the OpenTofu source. It informed this
ADR.

OpenTofu force-unlock works already (ADR 25, ADR 26), and the e2e suite proves
it. The Pulumi CLI already has `pulumi cancel` (ADR 20), `stack history`,
`stack export --version` (ADR 24) and `stack import`. The server has no list
of OpenTofu state versions. It has no restore for either tool. A client sees
only a flag for an OpenTofu workspace lock, and no holder for a Pulumi update.
ADR 20 asks RFC 18 to decide on a force-release action. ADR 24 and ADR 26 keep
every version until RFC 18 decides on retention. ADR 13 asks for tool-neutral
actions. ADR 17 asks for a REST API and an action on each route.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts the part of RFC 18 that covers history, restore and unlock.
Retention, content diff and the manual lock stay in RFC 18.

1. **Routes stay in the tool adapters.** Each new route is a new path or a new
   field in the adapter of its tool. No route changes an existing shape. A
   tool-neutral facade waits for the RFC for the OpenDustpan API.
2. **OpenTofu history.** The TFE adapter serves `GET /api/v2/state-versions`
   with the filters `filter[organization][name]` and
   `filter[workspace][name]`, and with `page[number]` and `page[size]`. It
   lists finalized state versions, newest first. Each entry has the TFE
   attributes and three added attributes: `holder`, `force` and
   `restored-from`. The route needs `unit:read` on the workspace unit. A new
   method `ListStateVersions` of the state store comes with conformance cases
   first.
3. **OpenTofu restore.** `POST /api/v2/state-versions/{state_version_id}/actions/restore`
   makes a new current state version from an old one. The route needs
   `unit:write` on the workspace unit.
   - The workspace must be unlocked or locked by the caller. A lock that
     another principal holds gives 409. The detail names the holder, in the
     words of ADR 25.
   - The server takes the lock for the call when it is free, and releases it at
     the end.
   - The server copies the content to a new blob key and checks the MD5 while
     it copies.
   - The new state version keeps the lineage, the serial, the MD5 and the size
     of the old one. It is a forced state version (`Force`), and it records
     `restored-from`.
   - The server never reads or rewrites state. OpenTofu takes its next serial
     from the bytes it reads. So the declared serial must equal the serial in
     the bytes. A restore therefore repeats a serial in the history, as
     `tofu state push -force` does.
   - A restore changes no infrastructure. The response and the console say so.
4. **Pulumi history.** The history response gains the fields `updateID`,
   `requestedBy`, `checkpointBytes`, `leaseExpiresAt` and `restoredFrom`. The
   existing fields stay. The update records the subject of the principal that
   created it. `checkpointBytes` is 0 when the update saved no checkpoint.
5. **Pulumi restore.** `POST /api/stacks/{namespace}/{project}/{stack}/restore/{version}`
   creates an update of kind `restore`, copies the checkpoint of that version
   into it and completes it. It follows the rules of `Updates.Import`. It needs
   `unit:write`. It respects the lease of an update in progress. It always
   ends the update that it starts. It answers 404 when the version has no
   checkpoint and 409 while another update runs. If a supported CLI version
   fails on the new kind, the kind is `import` and `restoredFrom` is the
   marker. Old ciphertext stays readable, because data keys never rotate
   (ADR 23).
6. **Unlock.** The existing routes are the force-release actions. For OpenTofu
   this is `force-unlock` with `unit:manage` (ADR 25). For the Pulumi CLI this
   is the cancel route with `unit:write` (ADR 20). The server adds no other
   force-release route. RFC 19 maps roles to both actions and can remove the
   difference.
7. **Lock view.** The TFE workspace resource gains the attributes
   `lock-holder`, `locked-at` and `lock-reason`. The Pulumi history response
   shows the running update with `requestedBy` and `leaseExpiresAt`. Both need
   `unit:read`.
8. **Retention.** The server keeps every version. This ADR adds no prune and no
   cleanup of orphan blobs. A later ADR decides them, with measured sizes. That
   ADR must keep any blob that a running backup can still copy (ADR 30). The
   history responses show sizes.
9. **Diff.** The server compares metadata only: serial, size, time and holder.
   A content diff needs RFC 39.
10. **Record of actions.** Each restore, force-unlock and cancel writes one
    structured log line with the action, the object, the principal and the
    released holder. The line never holds a request path, because the path of a
    state upload carries a capability (ADR 32). The rows keep `restored-from`,
    the holder and the requester, so that RFC 22 can fill the audit log. This
    ADR adds no audit port and no policy check. With no policy, the check
    allows the action (ADR 17).
11. **Console.** The console repository builds the views for the version list,
    the restore and the lock holder. The calls in decisions 2 to 7 are the API
    that it uses. The API ships first. The console says that a restore writes
    old state as a new version and changes no infrastructure.
12. **Order of work.** Every route lands with its e2e step first (ADR 12) and
    with its action in the route table (ADR 17).

## Consequences

- Open work:
  1. `StateStore.ListStateVersions` and a store write for a restored state
     version, with conformance cases and SQL adapters. New numbered migrations
     add `restored_from` to `state_versions`, and `created_by` and
     `restored_from` to `updates`.
  2. The TFE list route, the restore route and the lock attributes, with route
     tests.
  3. The Pulumi history fields, a join for the checkpoint size, and the restore
     route.
  4. A log line for each restore, force-unlock and cancel.
- A restore repeats a serial in the history of an OpenTofu workspace. The
  documents must say this.
- The server stores one full copy for each restore.
- The two force-release actions differ (`unit:manage` and `unit:write`). Before
  RFC 19, only administrators pass both.
- The history grows without limit until a later ADR decides on retention. A
  backup holds all versions (ADR 30).
- The names of the added fields are proposals. The e2e steps on all CLI
  versions must show that no CLI rejects them.
- The exit test of this part of milestone 3 is this. On Linux, for each
  OpenTofu version and each Pulumi version in the e2e matrix, on SQLite and on
  PostgreSQL, the e2e suite drives:
  1. The TFE list route after two `apply` runs, then a restore of the first
     state version, then `state pull` (same MD5) and an `apply` that succeeds.
  2. A restore while another principal holds the lock (409 with the holder),
     then `force-unlock`, then the restore.
  3. The lock attributes of a locked workspace.
  4. Two `up` runs, the history fields, a restore of version 1, `stack export`
     equal to `stack export --version 1`, and `up` after the restore.
  5. A restore during a running update (409), and `pulumi cancel` of a stuck
     update.

  The route tests find an action on every new route. The full Pulumi and
  OpenTofu scenarios still pass on the same server.
