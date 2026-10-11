# TFE-compatible API

OpenDustpan works with OpenTofu through a TFE-compatible API.
OpenTofu speaks this API through its `cloud` block and its `remote` backend.
This document lists the endpoints the server supports and the ways the
behavior differs from TFE. It is checked against the route table in
`api/tfe` by a test, so the list cannot go stale.

## Endpoints

| Method | Path | Access | What it does |
| --- | --- | --- | --- |
| GET | /.well-known/terraform.json | Public | Service discovery. Advertises `tfe.v2` and `tfe.v2.1` at `/api/v2/`. Lists no `login.v1`. |
| GET | /api/v2/ping | Public | Liveness probe. Sets `TFP-API-Version: 2.5`. |
| GET | /api/v2/organizations/{organization}/entitlement-set | Token, `namespace:read` | Reports `operations: false`. OpenTofu runs plans locally. |
| GET | /api/v2/organizations/{organization}/workspaces/{workspace} | Token, `unit:read` | Reads one workspace by name. Unknown names answer 404. |
| POST | /api/v2/organizations/{organization}/workspaces | Token, `unit:manage` | Creates one workspace. Duplicate names answer 422. |
| GET | /api/v2/workspaces/{id} | Token, `unit:read` | Reads one workspace by ID. Unknown IDs answer 404. |
| GET | /api/v2/organizations/{organization}/workspaces | Token, `namespace:read` | Lists the workspaces of one namespace the caller may `unit:read`. Supports the search query. `page[size]` caps at 100. |
| POST | /api/v2/workspaces/{id}/actions/lock | Token, `unit:write` | Locks one workspace for the caller. Locked workspaces answer 409. |
| POST | /api/v2/workspaces/{id}/actions/unlock | Token, `unit:write` | Releases the caller lock. Another holder answers 409. |
| POST | /api/v2/workspaces/{id}/actions/force-unlock | Token, `unit:manage` | Releases the lock whoever holds it. Unlocked workspaces answer 409. |
| GET | /api/v2/workspaces/{id}/current-state-version | Token, `unit:read` | Reads the current state version. Workspaces without state answer 404. |
| GET | /api/v2/state-versions | Token, `unit:read` | Lists the finalized state versions of the filtered workspace, newest first. Needs `filter[organization][name]` and `filter[workspace][name]`. Supports `page[number]` and `page[size]`. |
| GET | /api/v2/state-versions/{state_version_id} | Token, `unit:read` | Reads one finalized state version. Other versions answer 404. |
| GET | /api/v2/state-versions/{state_version_id}/download | Token, `unit:read` | Downloads the state document as JSON. |
| GET | /api/v2/workspaces/{id}/current-state-version-outputs | Token, `unit:read` | Reads the outputs of the current state. |
| GET | /api/v2/state-version-outputs/{output_id} | Token, `unit:read` | Reads one output by ID. Unknown outputs answer 404. |
| POST | /api/v2/workspaces/{id}/state-versions | Token, `unit:write` | Declares a state version. The caller must hold the lock. |
| PUT | /api/v2/state-uploads/{id}/{token} | Capability URL, no sign-in | Uploads the state document. The token is single use. |
| PUT | /api/v2/json-state-uploads/{id}/{token} | Capability URL, no sign-in | Accepts the JSON rendering and discards it. The token is single use. |

## Differences from TFE

The server keeps remote state and locks for local execution only.

- Execution is always local. The entitlement set reports `operations: false`.
- Every workspace reports `execution-mode: local`. The server runs no plans.
- Remote runs are out. There are no runs, no agents, and no policy checks.
- `tofu login` is out. The discovery document lists no `login.v1` service.
- Sign-in uses a static access token. The CLI sends it as `Bearer`.
- The token comes from the `cloud` block, a `credentials` block in the CLI
  configuration, or `TF_TOKEN_<host>`.
- An organization is a namespace. Unknown names answer 404.
- Unknown workspace and state version IDs answer 403 for a caller without
  rights, 404 otherwise, so the absence does not leak.
- The server never creates a namespace on its own.
- Workspace names are unique within one namespace. The ID has the prefix `ws-`.
- Rename is out. Delete is out.
- Workspace tags are out. The tag filter on the list is ignored.
- Variables are out. VCS-driven workspaces are out.
- Runs, run triggers, and notification settings are out.
- State upload uses capability URLs served by the API itself.
- The upload PUTs carry no `Authorization` header.
- There is no inline state fallback and no presigned object-storage URL.
- The lock request carries no client lock ID. The server mints the lock ID.
- The lock records the subject of the caller as the holder.
- A lock conflict answers 409. The detail holds `is locked by User` and the
  holder.
- A stale serial answers 422. Another lineage answers 422.
- A force upload skips the lineage check and the serial check.
- A force upload never skips the lock check.
- Errors are JSON:API documents with `status`, `title`, and `detail`.
- The media type is `application/vnd.api+json`.
