# 25. OpenTofu through the TFE-compatible API

Status: accepted · 2026-10-11. Extended by ADR 213 decision 10. Decision 6 superseded in part by ADR 239: a force-unlock needs `unit:write-forced`

## Context

[RFC 1](../rfc/0001-opentofu-via-the-tfe-compatible-api.md) proposes to
support OpenTofu through the TFE-compatible remote API. The `cloud` block and
the `remote` backend of OpenTofu speak this API. A brief of 2026-10-11 checked
the RFC against the code, the OpenTofu source and the go-tfe client. It
informed this ADR.

No TFE adapter exists. The `workspace` package exists, with no SQL adapter, no
conformance suite and no wiring. `dustpan.go` mounts the Pulumi handler at
`/api/`, and that pattern also matches `/api/v2/`. OpenTofu needs HTTPS for
service discovery, and ADR 27 covers TLS. ADR 26 covers the storage of state.
ADR 12 asks for one adapter for each tool. ADR 13 asks for an inventory
publish. ADR 17 asks for an action on each route. ADR 18 makes an
organization a namespace. ADR 19 gives a unit a stable ID.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts RFC 1 with the scope below.

1. **Scope of milestone 2.** The server keeps remote state and locks for local
   execution only. This is the `cloud` block and the remote backend. The
   server does not run plans or applies (RFC 30). It has no `tofu login` flow
   beyond a static token in the CLI configuration. It has no workspace tags
   and no run tasks (RFC 49). The Terraform CLI stays untested (RFC 53).
2. **Adapter.** The package `api/tfe` holds the HTTP adapter. It has its own
   route table and its own `NewHandler`. `dustpan.go` mounts it at `/api/v2/`
   with a pattern more specific than the Pulumi `/api/` pattern. A routing test
   proves that `/api/v2/ping` reaches the TFE adapter and that a Pulumi route
   reaches the Pulumi adapter. The route test of ADR 17 covers every `/api/v2`
   route. Adapters never call each other (ADR 12). Only `dustpan.go` knows
   both.
3. **Service discovery.** `GET /.well-known/terraform.json` advertises
   `tfe.v2` and is a public route. It lists no `login.v1`. `GET /api/v2/ping`
   is public and sets the header `TFP-API-Version: 2.5`. The route
   `entitlement-set` needs a token, as go-tfe sends one. It reports
   `operations: false`, so OpenTofu uses local execution. Workspaces report
   `execution-mode: local`. The public routes are on the explicit list of the
   route test.
4. **Organization.** A TFE organization is a namespace (ADR 18). An unknown
   organization answers 404 as a JSON:API error. The server never creates a
   namespace on its own.
5. **Workspace.** The name follows the existing rule of `workspace.Name`:
   1 to 90 characters. The ID has the prefix `ws-` (the rule of ADR 19). The
   unit reference is `opentofu:<namespace>/<name>`. Routes that take a
   workspace ID use the ID. A route by name resolves the name to the ID in a
   handler. Rename and delete wait for RFC 17.
6. **Actions (ADR 17).** Each route declares its action and its object:
   - A state read needs `unit:read`.
   - Lock, unlock and the creation of a state version need `unit:write`.
   - Workspace creation and force-unlock need `unit:manage`.
7. **Errors.** The adapter answers errors as JSON:API documents with
   `status`, `title` and `detail`. The media type is `application/vnd.api+json`.
   The detail of a lock conflict contains the words that go-tfe parses: "is
   locked by User", followed by the holder. A lock request on a locked
   workspace answers 409. An unlock by another principal answers 409. A
   force-unlock of a workspace that is not locked answers 409.
8. **Authentication.** The adapter reads `Authorization: Bearer <token>` and
   uses the same `identity.Authenticator` port as the Pulumi adapter. A wrong
   token answers 401.
9. **Upload URLs.** The adapter serves the state upload through capability
   URLs (ADR 26). It sends no inline upload fallback and no presigned URLs now.
10. **Inventory.** Each workspace write publishes to the inventory projection
    in the same transaction (ADR 13).
11. **Order of work.** Every endpoint lands with its e2e step first (ADR 12).
    The conformance suite for the `workspace` ports lands before the SQL
    adapters.

## Consequences

- Open work for milestone 2:
  1. A conformance suite for the `workspace` ports, SQL adapters for SQLite
     and PostgreSQL, and numbered migrations.
  2. The inventory projection of ADR 13. If it does not exist in the code when
     this work starts, it is part of this work. Each workspace write then
     publishes to it in its own transaction.
  3. The package `api/tfe`, its route test and its routing test.
  4. Wiring in `dustpan.NewServer`, with the namespace fence on every write
     (ADR 4).
  5. A review of the shared packages for tool-specific fields (ADR 12).
  6. A published list of the supported endpoints and of the differences from
     TFE.
- The e2e suite gets new steps: `destroy`, `state push`, lock conflict,
  `force-unlock`, `workspace new`, `workspace list` and `workspace select`.
  A step for a crashed `apply` is also new.
- The protocol carries no lock ID from the client. CI jobs that share one
  workspace need distinct principals.
- Workspace tags, `tofu login` and the `login.v1` service stay in the gaps
  section of RFC 1.
- The exit test of milestone 2 is this. On Linux, for each OpenTofu version
  in the e2e matrix, on SQLite and on PostgreSQL, over TLS, the e2e suite
  drives:
  1. `init`, `plan`, `apply`, `output` and `state pull`.
  2. `state push`. The server rejects a stale serial and accepts `-force`.
  3. A lock held by another principal, and `force-unlock`.
  4. Recovery after a crashed `apply`.
  5. `workspace new`, `workspace list` and `workspace select`.
  6. `destroy`.

  The full Pulumi scenario still passes on the same server.
