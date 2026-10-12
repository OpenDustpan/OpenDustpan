---
rfc: 65
title: OpenTofu through the http backend too
status: draft
created: 2026-10-12
---

# RFC 65. OpenTofu through the http backend too

Status: draft.

## Problem

ADR 25 serves OpenTofu through the TFE-compatible API, which the `cloud`
block and the `remote` backend use. That path has costs for some users:

- It needs HTTPS. Service discovery refuses plain HTTP, and the CLI trusts
  only the system certificate store. On Linux `SSL_CERT_FILE` can add a
  self-signed certificate. On macOS and Windows the certificate must be in
  the system store.
- It needs service discovery, a hostname and a token in the CLI
  configuration or `TF_TOKEN_<host>`.

OpenTofu's generic `http` backend has none of these requirements. It accepts
`http://` addresses, `skip_cert_verification`, and a CA in
`client_ca_certificate_pem`. The project owner asked on 2026-10-12 to support
both backends.

## What the http backend sends

From OpenTofu's source (`internal/backend/remote-state/http`, main branch on
2026-10-12):

- One address per state. The backend has no workspaces:
  `tofu workspace new`, `list` and `delete` fail with "workspaces not
  supported".
- `GET <address>`: 200 with the state, 204 or 404 for no state. The client
  checks a `Content-MD5` response header when the server sends one.
- `POST <address>` (method set by `update_method`): the whole state as the
  body, with `Content-MD5`. When the client holds a lock, it adds
  `?ID=<lock ID>` to the query. 200, 201 or 204 is success.
- `LOCK <lock_address>` (method set by `lock_method`): the lock info JSON as
  the body. 200 is success. 409 or 423 with the current lock info as the body
  is "locked by another".
- `UNLOCK <unlock_address>`: the lock info JSON. `tofu force-unlock` sends a
  body that holds only the lock ID. 200 is success.
- Credentials: HTTP basic auth (`username`, `password`, or `TF_HTTP_USERNAME`
  and `TF_HTTP_PASSWORD`), or any header from `headers`.
- The client does not send a force flag for `tofu state push -force`. That
  code is commented out upstream.

## Options

1. Do nothing. Users who cannot meet the TLS requirements cannot use
   OpenDustpan with OpenTofu.
2. A second OpenTofu adapter for the http backend, on the existing
   `workspace` domain package. Same workspaces, locks, state versions, fence
   and authorization as the TFE-compatible API.
3. A second OpenTofu adapter with its own domain package. This duplicates the
   OpenTofu model, which ADR 12 forbids for one tool.

## Proposal

Option 2.

1. **Adapter.** A new HTTP adapter, `internal/api/tofuhttp/`, with its own
   route prefix. It does not call `internal/api/tfe/`. Both adapters use
   `pkg/workspace`.
2. **Address.** `/tofu/state/{namespace}/{workspace}`. The same URL serves
   GET, POST, LOCK and UNLOCK, so `lock_address` and `unlock_address` are the
   address. A namespace is an organization, as in ADR 25.
3. **Same unit.** A workspace is one unit whatever adapter writes it. A team
   can move a workspace from one backend to the other with
   `tofu init -migrate-state`. The inventory shows one row for it.
4. **Credentials.** The password of basic auth is an OpenDustpan token. The
   server ignores the username. An `Authorization: Bearer <token>` header from
   `headers` also works. The docs tell users to set `TF_HTTP_PASSWORD`,
   because a password in the backend block is written into `.terraform` and
   into plan files.
5. **Create on first write.** A LOCK or POST on a workspace that does not
   exist creates it, with the same authorization action as the TFE create
   path. GET on a workspace that does not exist answers 404, which the client
   reads as no state.
6. **Lock fence.** A POST that carries `?ID=` must name the current lock. Any
   other ID answers 409 with the current lock info. A POST without `?ID=`
   while the workspace is locked answers 423. A POST without `?ID=` on an
   unlocked workspace is a `-lock=false` write. It needs
   `unit:write-unlocked` and the switches of ADR 44. An UNLOCK by a principal
   that does not hold the lock is a force-unlock. It needs
   `unit:write-forced` and writes the fence version of ADR 44. The lock ID
   check refuses a stale writer before the lineage check of ADR 44, so this
   path has two fences.
7. **State checks.** The serial and lineage rules of ADR 25 apply to each
   POST. The body streams through the size limit, the `Content-MD5` check and
   the seal of ADR 41 into the blob store, as ADR 45 says. GET answers
   `Content-MD5`.
8. **Outputs.** The http backend sends the state only, not
   `json-state-outputs`. The server reads the top-level `outputs` of the state
   in the same streaming pass, so the console and the outputs routes of the
   TFE-compatible API work for workspaces that this adapter writes.
9. **Plain HTTP.** The server warns at start when it serves plain HTTP, as
   ADR 27 says. It does not refuse plain HTTP, because the operator may end
   TLS in front of the server.
10. **e2e first.** The e2e suite drives the real `tofu` binary through the
    http backend before each route lands: init, apply, plan, a lock conflict,
    force-unlock, a stale write refused after force-unlock, and a state
    migration between the `cloud` block and the http backend in both
    directions.
11. **Tool name.** The tool is still "OpenTofu". The console shows no
    difference between the two adapters, except where it shows the
    configuration snippet for each.

## Open questions

- The adapter name and the route prefix. `tofuhttp` and `/tofu/state/` are
  proposals.
- Does the server need the `headers`-only path, or is basic auth enough?
- Does the outputs read of decision 8 belong in the workspace domain, with
  the TFE-compatible adapter changed to use it too? Then the server would no
  longer trust the `json-state-outputs` that the client sends.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
