# 32. Observing the server

Status: accepted · 2026-10-11. Probe paths superseded by ADR 34. Route paths and `server:` action names superseded in part by ADR 210

Changed by:

- ADR 34 changes decisions 3 and 8: the probe paths.
- ADR 60 changes decisions 1 and 6: OTLP logs and the request log to sinks.
- ADR 210 changes: the route paths and the server: action names.

## Context

[RFC 34](../rfc/0034-server-observability.md) asks how an operator watches an
OpenDustpan server. A brief of 2026-10-11 checked the RFC against the code. It
informed this ADR.

The server logs only start-up lines and some errors. It has no request log and
no metrics. `GET /healthz` answers 204 and checks nothing. The module has two
direct dependencies, and CLAUDE.md asks for the owner's yes before a new one.
ADR 17 asks each route to declare an action and an object. It also asks for an
explicit list of the routes that need no sign-in. ADR 30 adds the tool-neutral
adapter `api/server` for the installation, with the object `@server`.

The state upload URL of ADR 26 holds its credential in the path. A log of the
request path would leak it.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts RFC 34 with the scope below.

1. **Scope.** This ADR covers request logs, liveness, readiness and metrics.
   Rate limits go to a new RFC. Tracing stays in RFC 34 for later. The server
   does not use OpenTelemetry now.
2. **Metrics format.** The server writes the Prometheus text format, version
   0.0.4, with the standard library. The code is in `internal/metrics`. The
   server adds no dependency. A new ADR can change this when the metric set
   grows past about 15 families, or when the project needs OpenMetrics.
3. **Probes.** `GET /healthz` is liveness. It stays 204 and checks nothing.
   `GET /readyz` is readiness. It pings the store and the blob store with a
   2-second limit. It answers 200 with `{"status":"ok"}`, or 503 with the names
   of the failed checks and no error text. Both routes stay on the root mux and
   are public. Both are on the explicit public list of the route test. The
   store port and the blob port get a `Ping` method. Conformance tests land
   before the adapters implement it.
4. **Metrics endpoint.** `GET /api/server/metrics` is a route of the adapter
   `api/server` (ADR 30), on the main listener. It reads
   `Authorization: Bearer <token>` through the `identity.Authenticator` port.
   It declares the action `server:observe` on the object `@server`. An
   administrator has the action. The route test of ADR 17 covers the route. The
   server has no second listener now.
5. **Metrics set.** All names start with `dustpan_`. The set has these
   families:
   - the request count, the request duration and the requests in flight
   - `build_info` and `process_start_time_seconds`
   - goroutine, heap and garbage-collection values from `runtime/metrics`
   - `readiness_failures_total`

   ADR 33 adds the counter `deprecated_use_total`. The route label is the
   registered pattern, never the path. The adapter label is `pulumi`,
   `opentofu`, `inventory` or `server`. No label holds a namespace, a user, a
   token or a unit name. Store and blob counters, and lock and lease gauges,
   wait for a later ADR.
6. **Request log.** One middleware at the root mux writes one line for each
   request, with `method`, `route`, `status`, `duration_ms`, `bytes`,
   `request_id`, `adapter` and, for a signed-in request, `subject`. Probe and
   metrics requests log at `debug`. The server makes a new request ID for each
   request, returns it in `X-Request-Id`, and ignores any ID that the client
   sends. The flags `--log-level` and `--log-format` set the level (default
   `info`) and the format (`json` or `text`, default `json`).
7. **Never logged.** The server never writes these to a log, a metric or an
   error response: a credential header, a token, a request path or query, a
   request or response body, state, checkpoint or secret content, a key, the
   database URL, or driver error text. The log names the route pattern. For
   example, it never writes the path of a state upload, because that path is a
   capability. A test sends canary values through both tool adapters and fails
   when one appears in the log or in the metrics output.
8. **REST API.** `/healthz`, `/readyz` and `/api/server/metrics` are the
   documented API of this feature (ADR 17). The published endpoint list names
   them.
9. **Layering.** Domain packages never import the metrics package. Only
   `dustpan.go` and the HTTP middleware do.

## Consequences

- Open work for milestone 3:
  1. `internal/metrics` with the format test.
  2. The `Ping` method and its conformance tests in `storetest` and
     `blobtest`.
  3. The root middleware, the request ID and the two logging flags.
  4. The action `server:observe` and the metrics route in `api/server`, with
     its route test. ADR 30 adds the other routes of the same package. The first
     of the two changes creates the package. The public list of the route test
     gets `/healthz` and `/readyz`.
  5. The canary tests and the e2e steps for the metrics route and `/readyz`.
  6. Install docs: a Prometheus job with the `authorization` setting, and a
     note that the static admin token has every right until RFC 20.
- A scrape job holds the admin token until scoped tokens exist.
- Operators who want metrics on a private port must use a proxy rule for now.
- The image has no `HEALTHCHECK`. RFC 35 decides how the image checks itself.
- A new RFC covers rate limits. RFC 34 keeps tracing as an open item.
- The exit test of this part of milestone 3 is this. On Linux, on SQLite and on
  PostgreSQL, the e2e suite drives:
  1. After the Pulumi scenario and the OpenTofu scenario on one server,
     `GET /api/server/metrics` with the admin token returns
     `dustpan_http_requests_total` samples for both adapters. A request without
     a token gets 401.
  2. `/readyz` answers 200. On PostgreSQL, a stopped database turns `/readyz` to
     503 with the check name only, and `/healthz` stays 204.
  3. The canary test finds no canary value in the log or in the metrics output.
  4. Each response has a new `X-Request-Id`.

  The format test of the metrics output passes. The route test finds an action
  on the metrics route and lists `/healthz` and `/readyz` as public. The image
  smoke test checks `/readyz`.
