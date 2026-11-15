## Context

A fan-out change order holds one apply job for each of three workspaces. One apply fails. The server keeps the failure on its own workspace and keeps the prior current state version.

## Decisions

1. **Per-unit outcome record.** Keep one outcome for each apply job keyed by the workspace stable ID. The failed and cancelled outcomes hold the failure reason and no new current state version ID. Readers use the existing units list and the workspace state version list. The design defines no retry path. The result for units that did not fail and the change order status follow the RFC 115 decision once the owner decides it. The word cause stays reserved for the discovery job, principal, and fan-out reason of the record-names-cause change.

## Structure

Unexported type fanoutOutcome with fields changeOrderID, workspaceID, status and stateVersionID. Only the NEW constructor newFanoutOutcome builds it. It rejects an empty workspaceID and rejects a failed or cancelled status that carries a state version ID.

## Risks

- A reader shows the failure on the wrong workspace.
- A partial apply changes real infrastructure while the old state version stays current.
