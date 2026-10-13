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
Alert when it is above zero. The audit guide shows the rule.
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

## Send the application log to other targets

The server writes its log to stderr by default.
`--log-sink` (`DUSTPAN_LOG_SINK`) sends it to other targets instead (ADR 60 decision 14).
Give `--log-sink` once for each target.
In `DUSTPAN_LOG_SINK`, separate the targets with spaces or line breaks.
Each target is `[<name>=]<target>`:

| Target | Example |
| --- | --- |
| Standard error, in the format of `--log-format` | `stderr` |
| Standard output, in the format of `--log-format` | `stdout` |
| A file of JSON lines | `file:/var/log/dustpan/server.jsonl` |
| Syslog (RFC 5424) over UDP, TCP or TLS | `syslog+tls://logs.example:6514` |
| A signed HTTPS webhook | `webhook:https://logs.example/hooks/dustpan` |

```sh
opendustpan serve --log-sink stderr --log-sink 'syslog+tls://logs.example:6514'
```

Name `stderr` too when you want to keep the stderr lines.
A syslog target uses the facility `local0` (16) unless you set `?facility=<0 to 23>`.
`?max-size=<bytes>` caps a message, from 480 to 65507 bytes, default 8192.
The server drops a line above the cap and counts it.
A webhook target signs each request with the key in `DUSTPAN_LOG_WEBHOOK_SECRET`, or in a file that `DUSTPAN_LOG_WEBHOOK_SECRET_FILE` names.
The audit log guide describes the record format, the syslog format and the webhook signature.
Object storage is a target of the audit log only.

A target other than a stream is best effort.
The server puts each line in a queue of 4,096 lines and never waits for the target.
It writes the queue in batches, at least once a second.
When the queue is full or a write fails, the server drops the lines.
`dustpan_log_sink_dropped_total{sink="<name>"}` counts them.
Without a name, the kind is the name: `file`, `syslog` or `webhook`.
The server opens a file target again on `SIGHUP`, as it does for the audit log.

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
