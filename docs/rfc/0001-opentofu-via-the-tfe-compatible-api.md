---
rfc: 1
title: OpenTofu support through the TFE-compatible remote API
status: accepted
adr: 25
created: 2026-10-09
was: proposed under number 9
---

# RFC 1. OpenTofu support through the TFE-compatible remote API

Status: accepted as ADR 25. It was proposed under number 9 until ADR 15 moved every
undecided proposal to `docs/rfc/`.

## Context

Teams that run Pulumi often also run OpenTofu, and want one self-hosted
backend for both. OpenTofu can keep state remotely in three ways:

- the generic `http` backend: plain GET/POST of state plus LOCK/UNLOCK;
- the `remote` backend and the `cloud` block, both clients of the TFE API
  (JSON:API under `/api/v2`, the API of HCP Terraform and Terraform
  Enterprise).

Only the second gives users organizations, workspaces, `tofu login` and a
console, so it is what "native" support means here. As with the Pulumi CLI,
this API is not a published specification we can implement from; the e2e
suite runs real OpenTofu versions against the server.

What OpenTofu 1.13.1 does, from its source (`internal/cloud`) and the go-tfe
client it pins (v1.101.0), in order:

1. `GET /.well-known/terraform.json` on the configured hostname, looking for
   the `tfe.v2` service. HTTPS only. (Observed: OpenTofu 1.11.14, 1.12.7 and
   1.13.1 all stop here today with "Host does not provide a tfe service".)
2. `GET /api/v2/ping`, reading the `TFP-API-Version` response header. The
   `cloud` block requires 2.5 or later.
3. `GET /api/v2/organizations/{org}/entitlement-set`. With `operations: false`
   OpenTofu runs plans locally and uses the server for state and locking only.
4. Workspaces: read by name, create if missing (`POST
   /api/v2/organizations/{org}/workspaces`), list for `tofu workspace list`,
   projects list and create when a project is named.
5. Per operation: `POST /api/v2/workspaces/{id}/actions/lock`, read
   `current-state-version` and download it, create a state version, unlock.
   `tofu output` reads `GET /api/v2/workspaces/{id}/current-state-version-outputs`
   (and `state-version-outputs/{id}`), falling back to the full state only for
   outputs without a detailed type.
6. State upload: create the state version without content (attributes
   `serial`, `md5`, `lineage`, `force`, and base64 `json-state-outputs`), then
   `PUT` the state to `hosted-state-upload-url` and, when present, the JSON
   rendering to `hosted-json-state-upload-url`, in parallel. Those PUTs carry
   no `Authorization` header. Only if the server answers create with the error
   "param is missing or the value is empty: state" does OpenTofu fall back to
   sending base64 state inline.

Tokens arrive as `Authorization: Bearer <token>`, from the `cloud` block, a
`credentials` block in the CLI configuration, `TF_TOKEN_<host>`, or `tofu
login` (the `login.v1` service, an OAuth authorization-code flow).

## Decision

- Serve OpenTofu through a TFE-compatible API, in a new HTTP adapter
  `api/tfe/` next to `api/pulumi/`, plus the discovery document at
  `/.well-known/terraform.json`. Scope: the subset the `cloud` block and
  `remote` backend use for local execution (steps 1 to 6). Remote runs,
  agents, policy, variables and VCS-driven workspaces are out of scope.
- An organization is a namespace, exactly as for the Pulumi CLI (ADR 4,
  RFC 2). Namespace fencing, home regions and authorization apply unchanged.
- Workspaces are their own domain package, `workspace`, not stacks. Pulumi
  stacks are unique per project; TFE workspaces are unique per organization
  and only grouped by projects. State formats (Pulumi checkpoints versus
  tfstate with `lineage` and `serial`), locking (update leases versus
  workspace locks) and history differ too. The packages share namespaces,
  identity, the blob port and the fence, not models.
- Entitlements report `operations: false`, so every client runs locally.
  Workspaces report execution mode `local`.
- State versions are immutable blobs with metadata in SQL. A new version must
  have the current lineage and a higher serial (unless forced), checked in the
  same fenced transaction as the lock check when the version is finalized.
- Outputs are served from the `json-state-outputs` the client sends with each
  version, stored opaque; the server never derives them from state.
- The JSON state upload URL may accept and discard its body: the server does
  not need the plaintext rendering, which would defeat state encryption.
- Upload URLs are capability URLs: short-lived, single-use, scoped to one
  state version, served by the API itself. A future S3 blob adapter may hand
  out presigned URLs instead. The inline fallback is not used, because it
  depends on matching an error string.
- HTTPS is required for OpenTofu. The binary does not terminate TLS today, so
  the docs must cover a TLS-terminating proxy and the omnibus image must ship
  one or add `--tls-cert`/`--tls-key`.
- Naming: "works with OpenTofu" and "TFE-compatible API". HashiCorp's product
  names and marks are not ours. The suite tests OpenTofu only; whether to test
  against the Terraform CLI is a question for the
  maintainers.

## Consequences

- A second API family under the same server: `/api/v2/*` (JSON:API, `Bearer`
  tokens) next to `/api/*` (Pulumi, `token` scheme). Route prefixes do not
  collide, but the Pulumi handler must not swallow `/api/v2/`.
- The blob port gains its first heavy user before Pulumi checkpoints. Both
  should share one decision on blob layout and retention.
- `tofu login` needs browser sign-in and token minting from ADR 6 first;
  until then users configure a token in the CLI configuration.
- Each step lands with its e2e step first, as for Pulumi: discovery and ping,
  entitlements, workspace read and create, lock and unlock, state read, state
  create and upload, outputs.

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- `tofu login` and `terraform login`: the `login.v1` service in
  `/.well-known/terraform.json` (OTF, OpenTaco). Depends on browser sign-in
  (ADR 6).
- TLS: OpenTofu refuses plain HTTP; where TLS ends is not decided (RFC 35).
- Workspace selection by tags in the `cloud` block, and the tag filter on the
  workspace list (OTF, Terrakube).
- `tofu show` on a saved plan, import-only plans: compatibility bugs that OTF
  still has; add e2e steps.
- Publish the list of supported endpoints and every difference from TFE, as
  OTF's `tfe_api.md` does; stub what clients probe (`entitlement-set`,
  `/admin/feature-sets`).
- Remote execution is RFC 30; TFE `run-triggers` is RFC 24;
  `notification-configurations` is RFC 23.

## Outcome

Accepted as ADR 25 on 2026-10-11, for state and locking with local
execution. Remote runs (RFC 30), `tofu login`, workspace tags and run tasks
stay outside it.

Region terms in this RFC are superseded by [ADR 248](../adr/0248-federation-model.md): the server has no regions, and each namespace has a primary installation and replica installations.
