# 34. Probe paths

Status: accepted · 2026-10-11 · supersedes the probe paths in ADR 32

## Context

ADR 32 puts the liveness probe at `GET /healthz` and the readiness probe at
`GET /readyz`, on the root mux. Some Kubernetes ingress controllers serve
`/healthz` and `/readyz` for their own health. Such a controller sends these
paths to the application only after the Ingress and the Service of the
application are healthy. If an operator removes the Ingress or the Service by
mistake, a probe through the ingress can get the health of the controller, not
the health of OpenDustpan. The probe then shows a false signal.

No release of OpenDustpan has `/healthz` or `/readyz` yet. No supported CLI or
console uses them.

The project owner asked for this change on 2026-10-11.

## Decision

1. The probes go below their own prefix, `/health/`:
   - `GET /health/live` is liveness. It replaces `GET /healthz`. It stays 204
     and checks nothing.
   - `GET /health/ready` is readiness. It replaces `GET /readyz`. Its checks and
     its responses are as ADR 32 decision 3 gives them.
2. The server does not serve `/healthz` or `/readyz`. It keeps no alias, because
   an alias keeps the path that can show the false signal.
3. All other parts of ADR 32 stay. Where ADR 32 names `/healthz` or `/readyz`,
   read `/health/live` or `/health/ready`.

## Consequences

- The route test lists `GET /health/live` and `GET /health/ready` as public.
- The e2e suite and its binary mode wait for `GET /health/live`.
- Install docs and probe examples use the new paths.
- A new probe goes below `/health/`.
