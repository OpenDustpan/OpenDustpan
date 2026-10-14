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
| DELETE | /api/v2/organizations/{organization}/workspaces/{workspace} | Token, `unit:manage` | Soft-deletes one workspace (ADR 47): it leaves every route and list, its name is free at once, and its state stays until the final delete. Answers 204. A locked workspace answers 409, an unknown name 404. |
| GET | /api/v2/workspaces/{id} | Token, `unit:read` | Reads one workspace by ID. Unknown IDs answer 404. |
| GET | /api/v2/organizations/{organization}/workspaces | Token, `namespace:read` | Lists the workspaces of one namespace the caller may `unit:read`. Supports the search query. `page[size]` caps at 100. |
| POST | /api/v2/workspaces/{id}/actions/lock | Token, `unit:write` | Locks one workspace for the caller. Locked workspaces answer 409. |
| POST | /api/v2/workspaces/{id}/actions/unlock | Token, `unit:write` | Releases the caller lock. Another holder answers 409. |
| POST | /api/v2/workspaces/{id}/actions/force-unlock | Token, `unit:manage` | Releases the lock whoever holds it. Unlocked workspaces answer 409. A client-encrypted state keeps its lock unless the forced-write override is allowed (`unit:write-forced`, both switches on). |
| GET | /api/v2/workspaces/{id}/override-switches | Token, `unit:read` | Reads the unit switches `allow-forced-writes` and `allow-unlocked-writes`. Both are off by default. An override needs the namespace switch and the unit switch. |
| PUT | /api/v2/workspaces/{id}/override-switches | Token, `unit:set-overrides` | Sets the unit switches from a JSON body with the same two booleans. An empty or malformed body answers 400 and changes no switch. |
| GET | /api/v2/workspaces/{id}/current-state-version | Token, `unit:read` | Reads the current state version. Workspaces without state answer 404. |
| GET | /api/v2/state-versions | Token, `unit:read` | Lists the finalized state versions of the filtered workspace, newest first. Needs `filter[organization][name]` and `filter[workspace][name]`. Supports `page[number]` and `page[size]`. |
| GET | /api/v2/state-versions/{state_version_id} | Token, `unit:read` | Reads one finalized state version. Other versions answer 404. |
| GET | /api/v2/state-versions/{state_version_id}/download | Token, `unit:read` and `unit:read-secrets` | Downloads the state document as JSON. |
| GET | /api/v2/state-versions/{state_version_id}/resources | Token, `unit:read` | Lists the masked resources of one finalized state version with `page[cursor]` and `page[size]` and `filter[q]` as a substring of the address or the type: type `state-version-resources` with `address`, `type`, `provider`, `module` and `changed`. Paging applies after the filter. `meta` holds the next cursor and `incomplete`. A state the client encrypted answers 409. |
| GET | /api/v2/state-versions/{state_version_id}/resources/{address} | Token, `unit:read` | Reads one masked resource by address (path-escaped): its instances with masked `attributes` and the `masked` value paths. Unknown addresses answer 404. A state the client encrypted answers 409. |
| POST | /api/v2/state-versions/{state_version_id}/actions/reveal | Token, `unit:read` and `unit:read-secrets` | Reveals the plain value at one value path of a finalized state version: the body is a JSON:API document holding `path`, and the answer holds the plain `value` in a JSON:API document. The path must be in the `masked` set of the version, else the call answers 404. The call answers `Cache-Control: no-store` and writes one audit entry of the kind `reveal` before it answers. A state the client encrypted answers 409. The call reads the state in three passes and keeps the instance out of memory, with two limits. If the `sensitive_attributes` of the instance are over 1 MiB (1048576 bytes), the call answers 413. If the marks of the instance nest, end on one value, or go through a dynamic wrapper, the call needs the full instance, and it answers 413 for an instance over 4 MiB (4194304 bytes). |
| GET | /api/v2/state-versions/{state_version_id}/diff | Token, `unit:read` | Diffs two finalized state versions of one workspace with `from` and `page[cursor]` and `page[size]`: the path version is the `to` side. Type `state-version-diffs` with `added`, `removed` and `changed` (`address`, `changed-paths`, `changed-secrets`). `meta` holds the next cursor and `incomplete`. A `from` version of another workspace answers 404. A state either side encrypted answers 409. |
| GET | /api/v2/workspaces/{id}/current-state-version-outputs | Token, `unit:read` | Reads the outputs of the current state. |
| GET | /api/v2/state-version-outputs/{output_id} | Token, `unit:read` and `unit:read-secrets` | Reads one output by ID. Unknown outputs answer 404. |
| POST | /api/v2/workspaces/{id}/state-versions | Token, `unit:write` | Declares a state version. The caller must hold the lock. A full body budget answers 503 with `Retry-After: 1`. |
| POST | /api/v2/state-versions/{state_version_id}/actions/restore | Token, `unit:write` | Makes the version current again as a new, forced version. Unknown versions answer 404. Another holder answers 409. |
| PUT | /api/v2/state-uploads/{id}/{token} | Capability URL, no sign-in | Uploads the state document. The token is single use. The server checks the upload capability before it reserves budget, so a wrong capability never holds budget. A full body budget answers 503 with `Retry-After: 1`. |
| PUT | /api/v2/json-state-uploads/{id}/{token} | Capability URL, no sign-in | Accepts the JSON rendering, keeps only the JSON Pointer paths that `sensitive_values` marks, and discards the rest. The token is single use. The server checks the upload capability before it reserves budget, so a wrong capability never holds budget. A full body budget answers 503 with `Retry-After: 1`. |

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
- The masked resource routes answer the structure of a state version with
  each secret value as a marker holding its value path: outputs with
  `"sensitive": true`, paths in `sensitive_attributes`, and paths the kept
  `json-state` marks name. A version with no kept marks masks every
  attribute value of each resource instance. Only the reveal call, the
  download, and the single output route answer a secret value, each with
  `unit:read-secrets`; the reveal call writes one audit entry of the kind
  `reveal` before it answers. No other route answers a secret value,
  ciphertext, or sealed bytes.
- A state that the client encrypted answers 409 with `encrypted-by-client`
  on the resource routes. The outputs list still works for it.
- The diff answers the added, removed and changed resources by address with
  the resource-relative value paths of each change. A secret value is never
  in the diff: a changed, added or removed sensitive value is one path in
  `changed-secrets`. A path is sensitive when the masked read would mask it
  on either side. A `from` version of another workspace answers 404 as if it
  did not exist. A state either side encrypted answers 409.

## One token for each CI job

Give each CI job or runner that writes OpenTofu state its own token. The
server checks the lock holder by subject, and two clients that use one token
are one subject. So a client that shares the token of the lock holder can
write state under that lock, for example with `tofu apply -lock=false`, and
the server cannot refuse it. With distinct tokens, the server answers 409
with `is locked by User` and the holder. ADR 25 decided this rule. RFC 63
holds the open question of a stronger check.

Each token in `DUSTPAN_ADMIN_TOKEN` or in the token file signs in as its own
subject: `admin`, `admin-2`, and so on. These limits apply today:

- Each token is a full administrator.
- The server reads the tokens only when it starts. A change to the list
  needs a restart.

Scoped access tokens are RFC 20. The
[configuration reference](reference/configuration.md) lists the token
settings. See [ADR 25](adr/0025-opentofu-through-the-tfe-compatible-api.md),
[RFC 63](rfc/0063-workspace-lock-holder.md) and
[RFC 20](rfc/0020-access-tokens.md).

## State at rest

The server seals each new state blob and each new outputs document with the
data key of the workspace scope, under the key-encryption key in
`DUSTPAN_KEY_ENCRYPTION_KEY` or the file in
`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`. The MD5 and the size of a state version
stay those of the plain bytes. A download answers the same bytes the CLI
sent. A lost key means lost OpenTofu state, and nobody can recover it. A
backup holds the sealed bytes: keep the key apart from the backup target.
