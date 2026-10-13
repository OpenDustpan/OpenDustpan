# 49. OpenTofu also connects through the http backend

Status: accepted · 2026-10-13

## Context

[RFC 65](../rfc/0065-opentofu-http-backend.md) found that the
TFE-compatible API of ADR 25 needs HTTPS, service discovery and a
certificate in the system store. OpenTofu's generic `http` backend needs
none of these. On 2026-10-12 the project owner asked: "Ugh, can we just
support both backends?" On 2026-10-13 the owner accepted the recommended
answers to the open questions of RFC 65.

## Decision

1. **A second OpenTofu adapter.** The server serves OpenTofu's `http`
   backend from a new HTTP adapter, `internal/api/tofuhttp/`. It does not
   call the TFE-compatible adapter. Both adapters use `pkg/workspace`
   (ADR 12).
2. **One address for each workspace.** The address is
   `/tofu/state/{namespace}/{workspace}`. The same address serves GET, POST,
   LOCK and UNLOCK, so `lock_address` and `unlock_address` are the address.
3. **One unit.** A workspace is one unit whatever adapter writes it. A team
   can move a workspace between the two backends with
   `tofu init -migrate-state`. The inventory shows one row for it.
4. **Two ways to send a token.** The server accepts an OpenDustpan token as
   the password of HTTP basic auth, and ignores the username. It also
   accepts an `Authorization: Bearer <token>` header, which the backend
   sends through `headers`. The docs tell users to set `TF_HTTP_PASSWORD`,
   because a password in the backend block goes into `.terraform` and into
   plan files.
5. **Create on first write.** A LOCK or a POST on a workspace that does not
   exist creates it, with the authorization action of the TFE-compatible
   create path. A GET on a workspace that does not exist answers 404, which
   the client reads as no state.
6. **The lock fence of ADR 44 applies.** A POST with `?ID=` must name the
   current lock. Another ID answers 409 with the current lock info. A POST
   without `?ID=` on a locked workspace answers 423. A POST without `?ID=`
   on an unlocked workspace is a write without the lock: it needs
   `unit:write-unlocked` and the switches of ADR 44. An UNLOCK by a
   principal that does not hold the lock is a force-unlock: it needs
   `unit:write-forced` and writes the fence version of ADR 44.
7. **The state checks of ADR 25 apply.** Each POST passes the serial and
   lineage checks. The body streams through the size limit, the
   `Content-MD5` check and the seal of ADR 41 into the blob store (ADR 45).
   A GET answers `Content-MD5`.
8. **The server reads the outputs from the state.** The `workspace` domain
   reads the top-level `outputs` of a state while the state streams in. The
   TFE-compatible adapter uses the same read. The server no longer uses the
   `json-state-outputs` that a client sends for the outputs that it shows.
   The TFE-compatible routes still accept that upload, so the API contract
   does not change.
9. **Plain HTTP gives a warning.** The server warns at start when it serves
   plain HTTP (ADR 27). It does not refuse plain HTTP, because the operator
   can end TLS in front of the server.
10. **e2e first.** Before each route lands, the e2e suite drives the real
    `tofu` binary through the http backend: init, apply, plan, a lock
    conflict, force-unlock, a stale write refused after force-unlock, and a
    state migration between the `cloud` block and the http backend in both
    directions.
11. **The tool name stays "OpenTofu".** The console shows the same views for
    both adapters. It shows a different configuration snippet for each.

## Consequences

- Users who cannot meet the TLS requirements of the TFE-compatible API can
  use OpenDustpan with OpenTofu.
- The `http` backend has no workspaces. Each workspace needs its own
  backend block or `-backend-config`.
- `tofu state push -force` sends no force flag through either backend, so a
  forced write from the CLI stays refused. ADR 50 records how an
  administrator makes a forced write.
- The outputs read moves into the domain. A change to the TFE-compatible
  adapter lands with it, and the e2e suite checks the outputs of both
  adapters.
