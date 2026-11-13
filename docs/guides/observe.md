# Observe the server

This guide shows how to check liveness and readiness, scrape metrics, and read request logs.
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

The static admin token holds every right until a later change, so the scrape job holds that token.
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
| `dustpan_audit_sink_entries_total` | `sink` |
| `dustpan_audit_sink_failures_total` | `sink`, `reason` |
| `dustpan_audit_sink_skipped_total` | `sink` |
| `dustpan_audit_sink_lag_seconds` | `sink` |
| `dustpan_audit_sink_lag_entries` | `sink` |
| `dustpan_config_reloads_total` | `result` |
| `dustpan_config_last_reload_success` | (none) |
| `dustpan_inactivity_sweeps_total` | `result` |
| `dustpan_deleted_unit_sweep_total` | `result` |
| `dustpan_signin_failure_counter_errors_total` | `operation` |
| `dustpan_blob_purge_pending` | (none) |
| `dustpan_blob_purge_oldest_age_seconds` | (none) |
| `dustpan_blob_purge_read_errors_total` | (none) |

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
We designed this alert against NIST SP 800-53 AU-5 and PCI DSS v4.0.1
requirement 10.7, which ask for an alert when audit logging fails. This is not
a compliance claim.
`dustpan_audit_sink_failures_total` counts the failed deliveries of an audit
sink, with the labels `sink` (the sink name) and `reason` (`size` for an
entry the sink refused because it cannot hold it, `unreachable` when the
sink never reached its receiver, `write` for any other failed write, `read`
for a failed entry, cursor or namespace read, and `lease` for a failed sink
lease take).
A refused entry logs one error line per entry with the sink name, its scope
and its sequence.
Any other failure logs one error line per round with the sink name, the
reason, and the sequence of the first entry of the failed batch when the
round knows it.
A round that never read a batch, for example a failed lease take or a failed
entry, cursor or namespace read, carries no sequence.
We designed this alert against NIST SP 800-53 AU-5 and PCI DSS v4.0.1
requirement 10.7. The audit log in the database keeps every entry, and the sink
is its live copy.
`dustpan_audit_sink_lag_entries` holds the count of entries a sink has not
delivered yet, or 0 when the sink is up to date.
Alert when it keeps growing: the sink falls behind.
The audit guide shows the rules.

```yaml
groups:
  - name: dustpan
    rules:
      - alert: DustpanAuditSinkRefusedEntry
        expr: increase(dustpan_audit_sink_failures_total{reason="size"}[15m]) > 0
        labels:
          severity: critical
        annotations:
          summary: An OpenDustpan audit sink refused an entry it cannot hold
      - alert: DustpanAuditSinkLagging
        expr: dustpan_audit_sink_lag_entries > 50
        labels:
          severity: warning
        annotations:
          summary: An OpenDustpan audit sink falls behind
```
`dustpan_signin_failure_counter_errors_total` counts the failed calls of
the failure budget that all server processes share, with the label `operation`
(`add` or `prune`).
While these calls fail, each server process keeps only its own budget.
`dustpan_config_reloads_total` counts the reloads of the configuration file
with the label `result` (`success` or `failure`).
`dustpan_config_last_reload_success` holds 1 when the last reload succeeded,
and 0 when it failed. It holds 1 at start. Alert when it holds 0: the server
kept its old configuration, and the log line `config file reload failed`
names the key and the rule.
`dustpan_inactivity_sweeps_total` counts the runs of the inactivity disable.
The `result` label is `success` or `failure`.
Alert when the `failure` series grows: a failed run disables nothing.
`dustpan_deleted_unit_sweep_total` counts the outcomes of the retention
expiry. The `result` label is `purged`, `skipped`, `finished` or
`failed`. `finished` counts the blob purge records that the run finished.
`failed` counts the failed purges, blob deletes and record finishes, and the
runs that could not read the store.
Alert when the `failed` series grows: a failed purge keeps the unit until the
next run. The log line of each failure names the unit and the error kind.

`dustpan_blob_purge_pending` holds the number of purged units whose blobs are
not deleted yet. Each such unit has a blob purge record until the retention
sweep finishes it.
`dustpan_blob_purge_oldest_age_seconds` holds the age in seconds of the oldest
of these records, or 0 when none exists.
Both gauges count every namespace. They have no label.
The sweep deletes the blobs of a record only after a grace period of 24 hours.
So an age of up to one day is normal. An age that keeps growing past one day
shows that the sweep does not finish the records.
Each scrape reads the store. Both gauges share one read. The read stays valid
for 2 seconds after it completes, so a slow read still supplies both gauges.
A failed read exports 0 for both gauges. The worker gauges behave the same way.
`dustpan_blob_purge_read_errors_total` counts the failed reads of the two blob
purge gauges, so an alert on it shows a store outage that the two gauges hide.

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
`--log-sink` (`DUSTPAN_LOG_SINK`) sends it to other targets instead.
Give `--log-sink` once for each target.
In `DUSTPAN_LOG_SINK` and in the key `log-sink` of the configuration file, separate the targets with spaces or line breaks.
The start log shows only whether `log-sink` is set, because a webhook URL can hold a token.
Each target is `[<name>=]<target>`:

| Target | Example |
| --- | --- |
| Standard error, in the format of `--log-format` | `stderr` |
| Standard output, in the format of `--log-format` | `stdout` |
| A file of JSON lines | `file:/var/log/dustpan/server.jsonl` |
| Syslog (RFC 5424) over UDP, TCP or TLS | `syslog+tls://logs.example:6514` |
| A signed HTTPS webhook | `webhook:https://logs.example/hooks/dustpan` |
| OTLP logs over HTTPS | `otlp:https://collector.example:4318` |

```sh
opendustpan serve --log-sink stderr --log-sink 'syslog+tls://logs.example:6514'
```

Name `stderr` too when you want to keep the stderr lines.
A syslog target uses the facility `local0` (16) unless you set `?facility=<0 to 23>`.
`?max-size=<bytes>` caps a message: from 480 to 65507 bytes over UDP, and from 480 to 65536 bytes over TCP and TLS.
The default is 8192 over UDP and 65536 over TCP and TLS.
Raise the matching limit of the receiver to at least the same size.
With rsyslog set `$MaxMessageSize`. On a TCP input also set `MaxFrameSize`.
The defaults of both may be below 64 KiB. Check the receiver's limit.
With syslog-ng set `log-msg-size()`. Its default may be below 64 KiB. Check the receiver's limit.
The sink never shortens a field and never sends a changed entry.
The sink refuses a line above the cap and never sends it.
A refused line is skipped and counted in `dustpan_log_sink_dropped_total`.
The audit log guide states the full guarantee for audit entries.
A webhook target signs each request with the key in `DUSTPAN_LOG_WEBHOOK_SECRET`, or in a file that `DUSTPAN_LOG_WEBHOOK_SECRET_FILE` names.
An OTLP target sends the lines to `<URL>/v1/logs` as OTLP/HTTP with the JSON encoding.
Set the headers of the target, for example an API key, in `DUSTPAN_LOG_OTLP_HEADERS`, or in a file that `DUSTPAN_LOG_OTLP_HEADERS_FILE` names.
The audit log guide describes the record format, the syslog format, the webhook signature and the OTLP format.
Object storage is a target of the audit log only.

A target other than a stream is best effort.
The server puts each line in a queue of 4,096 lines and never waits for the target.
It writes the queue in batches, at least once a second.
When the queue is full or a write fails, the server drops the lines.
`dustpan_log_sink_dropped_total{sink="<name>"}` counts them.
Without a name, the kind is the name: `file`, `syslog`, `webhook` or `otlp`.
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

These items are not in this release.

- Rate limits.
- Tracing.
- A second listener.
