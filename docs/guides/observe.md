# Observe the server

This guide shows how to check liveness and readiness, scrape metrics, and read request logs.
It follows ADR 32.
Probe paths follow ADR 34.
The end-to-end suite covers the metrics route and both probes.

## Liveness and readiness

`GET /health/live` is liveness.
It checks nothing and answers 204 with no body.
It needs no sign-in.
`GET /health/ready` is readiness.
It pings the store and the blob store.
Each ping gets 2 seconds.
It answers 200 with `{"status":"ok"}` when each ping succeeds.
It answers 503 with the names of failed checks and no error text.
Failed checks are `store` and `blob`.
The body has media type `application/json`.
Both probes stay public and need no sign-in.
Both log at `debug`.

## Health command and the container image

`opendustpan health` probes `GET /health/ready` on the local server.
It prints `ready` and exits 0 on a 2xx answer.
It exits 1 on each other result.
It reads `--listen` (`DUSTPAN_LISTEN`) and `--tls-cert` (`DUSTPAN_TLS_CERT`).
With `--tls-cert` set, it probes over HTTPS.

```sh
opendustpan health --listen :8080
```

The image defines a `HEALTHCHECK` that runs this command every 30 seconds.
Read the status with `docker inspect --format '{{.State.Health.Status}}' dustpan`.
Kubernetes ignores `HEALTHCHECK` and uses its own probes.

## Metrics endpoint

`GET /api/server/metrics` writes the registry in the Prometheus text format.
It needs `Authorization: Bearer <token>`.
It declares the action `server:observe` on the object `@server`.
An administrator holds the action.
A request without a token answers 401.
The media type is `text/plain; version=0.0.4`.

```sh
curl -H "Authorization: Bearer $DUSTPAN_ADMIN_TOKEN" https://dustpan.example/api/server/metrics
```

Scrape it with a Prometheus job that uses the `authorization` setting.

```yaml
scrape_configs:
  - job_name: dustpan
    metrics_path: /api/server/metrics
    authorization:
      credentials: <admin token>
    static_configs:
      - targets: ['dustpan.example:8080']
```

The static admin token holds every right until RFC 20, so the scrape job holds that token.
The server has no second listener.
Serve metrics on a private port through a proxy rule for now.

## Metric families

| Family | Labels |
| --- | --- |
| `dustpan_http_requests_total` | `adapter`, `route`, `method`, `code` |
| `dustpan_http_request_duration_seconds` | `adapter`, `route`, `method` |
| `dustpan_http_requests_in_flight` | `adapter` |
| `dustpan_build_info` | `version` |
| `dustpan_process_start_time_seconds` | (none) |
| `dustpan_go_goroutines` | (none) |
| `dustpan_go_heap_bytes` | (none) |
| `dustpan_go_gc_cycles_total` | (none) |
| `dustpan_readiness_failures_total` | (none) |
| `dustpan_audit_append_failures_total` | (none) |

The route label holds the registered pattern, never the path.
A request that matches no pattern uses `unmatched`.
The adapter label is `pulumi`, `opentofu`, `inventory` or `server`.
No label holds a namespace, a user, a token or a unit name.
The duration family also writes `_bucket`, `_sum` and `_count` series.
`dustpan_build_info` holds 1 with the server version.
`dustpan_process_start_time_seconds` holds the start time in seconds.
`dustpan_audit_append_failures_total` counts audit entries lost because the
append failed.
Alert when it is above zero; the audit guide shows the rule.
This is the alert for failed logging (PCI DSS 10.7).

## Request log

One middleware writes one line for each request.
Each line holds `method`, `route`, `status`, `duration_ms`, `bytes`, `request_id`, `adapter` and, for a signed-in request, `subject`.
The route is the pattern only, never the path.
Probe and metrics requests log at `debug`.
`--log-level` (`DUSTPAN_LOG_LEVEL`) sets the level.
The default is `info`.
Values are `debug`, `info`, `warn` and `error`.
`--log-format` (`DUSTPAN_LOG_FORMAT`) sets the format.
The default is `json`.
Values are `json` and `text`.

## Request IDs

The server mints a new request ID for each request.
It returns the ID in `X-Request-Id`.
It ignores an ID that the client sends.

## What the server never logs

The server never writes these values to a log, a metric or an error response:

- A credential header or a token.
- A request path or query.
- A request or response body.
- State, checkpoint or secret content.
- A key or the database URL.
- Driver error text.

A test sends canary values through both tool adapters and fails when one appears in the log or in the metrics output.

## What this guide does not cover

These items are not in milestone 3.

- Rate limits.
- Tracing.
- A second listener.
