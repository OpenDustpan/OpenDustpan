---
rfc: 92
title: OpenTelemetry for jobs, with job traces, fleet metrics and OTLP export to the operator's collector
status: draft
created: 2026-10-20
---

# RFC 92. OpenTelemetry for jobs, with job traces, fleet metrics and OTLP export to the operator's collector

Status: draft. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end.

## Problem

An operator who has an OpenTelemetry collector cannot see an OpenDustpan job
as one trace. The job record has the times of each state and phase, and the
tool can emit its own spans, but nothing joins them. Nothing ties a span of
the tool to the job that started it, to the commit behind it, or to the
state version that it wrote.

The operator also cannot answer fleet questions in the tools that they
already use: which plans are slowest, where waits for a lock pile up, the
failure rate, drift, and the lead time from a merge to an apply.

This RFC asks:

1. How the trace context goes into the tool, and where the spans of the
   tool go.
2. Which spans the server makes, and with which code (a new dependency or
   not).
3. Which fleet metrics come from data that we hold, and where each one
   lives: Prometheus, a span or the REST API.
4. How the change-delivery metrics of a collection are defined, and which
   facts we lack.
5. How export to the operator's collector is configured and secured.

In this RFC, a "run" of the theme issue is a job (the glossary avoids "run"
alone), or a state write from a team's own CI with no job.

## What is decided already

- **ADR 32 decision 1:** "The server does not use OpenTelemetry now."
  Tracing stays open in RFC 34. Decision 5: no metric label holds a
  namespace, a user, a token or a unit name. Decision 7: the list of values
  that are never logged applies to every output. Decision 9: domain
  packages never import the metrics package.
- **ADR 60 decision 12:** the `otlp` audit and log sink sends OpenTelemetry
  logs over OTLP/HTTP with the JSON encoding, with the standard library and
  no dependency. Decision 15: a secret of a sink (for example an OTLP
  header) comes from an environment variable or a file, never from a flag.
- **ADR 74:** the server sends no telemetry. Decision 3: it connects out
  only to targets that the operator names. Decision 4: metrics, logs and
  traces are observability, not telemetry, and go only to targets that the
  operator configures.
- **ADR 100:** a job is a list of phases, each phase is one durable
  operation, and the job states hold the waits (`queued` with the wait
  reason `unit-busy` or `frozen`, `awaiting-approval`). Decision 20: the
  job token, and the state version that a job wrote (`StateVersion.Job`).
- **ADR 101 decision 7 and ADR 134 decision 6:** a step gets a fixed
  environment. The launcher sets the `DUSTPAN_` variables. A step variable
  that the credential-variable rule (`job.IsCredentialEnv`) matches fails
  the step. **ADR 101 decision 10:** no credential in a variable.
- **ADR 147 decisions 9 to 12:** a job is the change record, a state write
  with no job is recorded with its actor, and a person links a job to an
  incident with the role `cause`, `fix` or `related`. ADR 147 says
  "designed against" DORA (the EU regulation). This RFC never uses "DORA
  metrics" for the delivery metrics, so the two cannot be confused.
- **ADR 210 and ADR 212 decision 7:** the tool-neutral REST API is under
  `/api/dustpan/v1/`, the installation actions have the area
  `installation:`, and installation settings are one resource with views.
  A secret setting that only the static layer can set answers 409 to a
  `PATCH`.
- **ADR 250:** every input from outside the process is untrusted, with
  five layers and one test for each.

## ADRs this RFC would amend

- ADR 32 decision 1 ("The server does not use OpenTelemetry now"). An ADR
  that accepts this RFC supersedes that sentence.
- ADR 134 decision 6: new launcher variables for the tool step (`OTEL_*`
  and `TRACEPARENT`). They do not start with `DUSTPAN_`, so the rule
  "a step variable that a variable of the launcher also sets fails the
  step" must cover them.
- ADR 101 decision 7: the credential-variable rule. `OTEL_EXPORTER_OTLP_HEADERS`
  and `OTEL_EXPORTER_OTLP_TRACES_HEADERS` can hold an authorization header,
  and today the rule does not match them (no listed suffix). Add them.

## Facts from primary sources

Checked on 2026-10-20, against the docs and the source of the default
branch of each project.

### OpenTofu

- **Version.** 1.10.0 added tracing. The changelog says: "OpenTofu can now
  produce partial OpenTelemetry trace information, sent to a collector
  endpoint you control"
  (<https://raw.githubusercontent.com/opentofu/opentofu/v1.10/CHANGELOG.md>).
  The latest stable tag is v1.13.1.
- **Status.** The docs say that tracing "is experimental and may change",
  is "completely disabled by default", and samples 100% of traces
  (<https://opentofu.org/docs/internals/tracing/>). The source says: "This
  is not a committed external interface... Do not depend on anything about
  the structure of this output"
  (<https://github.com/opentofu/opentofu/blob/main/internal/tracing/init.go>).
- **Variables.** Tracing is on only with `OTEL_TRACES_EXPORTER=otlp`. The
  exporter comes from `go.opentelemetry.io/contrib/exporters/autoexport`,
  so the standard OTLP exporter variables apply: `OTEL_EXPORTER_OTLP_ENDPOINT`,
  `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_HEADERS` and
  `OTEL_EXPORTER_OTLP_INSECURE`. The docs name the endpoint and `INSECURE`.
- **`TRACEPARENT`.** `init.go` reads `TRACEPARENT` and `TRACESTATE` and
  extracts them with the W3C Trace Context propagator, so the spans of
  OpenTofu join the parent trace. The code is in the v1.10 and v1.11
  branches too. The docs page does not say so. From 1.13.0, the
  `local-exec` provisioner sets `TRACEPARENT` for its child processes
  (<https://raw.githubusercontent.com/opentofu/opentofu/v1.13/CHANGELOG.md>).
- **Signals.** Traces only. The code sets up a span exporter and no metric
  exporter.
- **Spans.** Names in the source include "Init", "Initialize Backend",
  "Get State from Backend", "Install Providers", "Get Providers",
  "Get Modules", "Install Registry Module", "Get Schemas",
  "Import", "Show Plan", "Unlock", and spans for ephemeral resources. HTTP
  client spans come from `otelhttp`. The docs say that traces cover
  provider downloads, plan and apply. We found no span named "Plan" or
  "Apply" in the source. This is not checked further.
- **Attributes** (`internal/tracing/traceattrs/opentofu.go`):
  `opentofu.provider.address`, `opentofu.provider.version`,
  `opentofu.target_platform`, `opentofu.module.name`,
  `opentofu.module.source`, `opentofu.module.version` and
  `opentofu.oci.*`, plus the HTTP attributes of semantic conventions 1.27.
- **Sensitive data.** The docs give no warning. We read these attributes as
  able to hold sensitive values: a module source can be a Git URL with a
  credential in it, and an HTTP span can hold a URL with a signed query.
  No attribute that we found holds a resource value. The source is not a
  committed interface, so a later release can add one.

### The Pulumi CLI

- **`--tracing` (older).** "Emit tracing to the specified endpoint. Use the
  `file:` scheme to write tracing data to a local file"
  (<https://www.pulumi.com/docs/iac/cli/commands/pulumi/>). The scheme
  selects the back end (`sdk/go/common/util/cmdutil/trace.go`): `file:`
  writes an appdash store, `tcp:` sends to an appdash collector, and any
  other URL sends Zipkin over HTTP. This is OpenTracing, not OTLP.
- **`--otel-traces` (newer).** Added in 3.226.0 (2026-03-11), per the
  changelog (<https://github.com/pulumi/pulumi/blob/master/CHANGELOG.md>):
  "Use file:// for local JSON files, grpc:// or https:// for remote
  collectors". The schemes are `file`, `grpc`, `grpcs`, `http` and
  `https`. HTTP sends `application/x-protobuf`. 3.255.0 added `https`.
  No environment variable turns it on: it is a flag only.
- **`TRACEPARENT`.** From 3.229.0: "Allow TRACEPARENT to be set for CLI
  invocations, parenting the pulumi spans under an existing parent span".
  It works only with `--otel-traces`. From 3.268.0 the CLI sends the W3C
  `traceparent` header on its API calls when `--otel-traces` is set.
- **Plugins.** The CLI runs a local OTLP receiver and passes its address to
  plugins (`PULUMI_OTEL_EXPORTER_OTLP_ENDPOINT`), so provider spans join
  the trace of the CLI.
- The latest version is 3.270.0 (2026-10-16).

### Terragrunt

- **Exporters** (<https://docs.terragrunt.com/troubleshooting/open-telemetry/>):
  `TG_TELEMETRY_TRACE_EXPORTER` takes `none` (the default), `console`,
  `otlpHttp`, `otlpGrpc` or `http`. `TG_TELEMETRY_METRIC_EXPORTER` takes
  `none`, `console`, `otlpHttp` or `grpcHttp`. Each has an
  `..._INSECURE_ENDPOINT` variable. So Terragrunt sends traces and metrics.
- **`TRACEPARENT`.** Terragrunt reads `TRACEPARENT` as its parent
  (`internal/telemetry/tracer.go`), and sets `TRACEPARENT` in the
  environment of each command that it runs, OpenTofu included
  (`internal/shell/run_cmd.go`).
- **Version.** The theme issue says 0.72.0. We did not check the first
  version. The latest tag is v1.1.6.
- **Sensitive data.** The docs give no warning. The examples show command
  arguments and directory paths as attributes.

### OpenTelemetry Go and the protocols

- `go.opentelemetry.io/otel` is under the Apache License 2.0. The latest
  tag is v1.47.0. The go.mod of `otlptracehttp` requires, beside the OTel
  modules, `go.opentelemetry.io/proto/otlp`, `google.golang.org/grpc`,
  `google.golang.org/protobuf`, `cenkalti/backoff/v5` and `go-logr/logr`.
  Indirect: `grpc-gateway/v2`, `genproto` (`googleapis/api`,
  `googleapis/rpc`), `x/net` and others. Code that is not a test links
  gRPC through `internal/otlpconfig`. The HTTP exporter sends
  `application/x-protobuf` only.
- OTLP/HTTP accepts "Protobuf payloads encoded either in binary format or
  in JSON format", at `/v1/traces` on port 4318 by default
  (<https://opentelemetry.io/docs/specs/otlp/>).
- W3C Trace Context Level 1 (<https://www.w3.org/TR/trace-context/>):
  `traceparent` is `00-<32 hex trace ID>-<16 hex parent ID>-<2 hex flags>`.
  An all-zero ID is invalid.
- Environment variables as context carriers
  (<https://opentelemetry.io/docs/specs/otel/context/env-carriers/>) has
  the status "Release Candidate". The key is `TRACEPARENT` in uppercase.

**What these facts mean here.** All three tools read `TRACEPARENT`, so a
job can join their spans to its trace (option A2). But OpenTofu and the
Pulumi CLI send OTLP as protobuf, not JSON, so a relay that receives their
spans must read protobuf (question 3). The Pulumi CLI can also write
JSON files, so a file path (option A1.3) works for it. No tool documents
which attributes can hold sensitive values, and the OpenTofu output is not
a committed interface. So the attribute allowlist must be tested against
each pinned CLI version.
## Options and recommendations

### A. The job trace and the trace context in the tool

The server makes the trace ID of a job at the start of the job: 16 random
bytes from `crypto/rand`, stored in the job record. The job is the root
span. Each state with a wait (`queued`, `awaiting-approval`) and each phase
is a child span. A phase of a runner has one span for each step, which the
launcher measures. The state write of the job is a child span of the
phase that wrote it.

**A1. Where the spans of the tool go.**

1. **To the operator's collector, from the job.** The launcher sets
   `OTEL_EXPORTER_OTLP_ENDPOINT` to the collector. The job then needs
   egress to the collector, and the auth header of the collector reaches
   the job in a variable (against ADR 101 decision 10). Code of the job
   (a provider, a module, a Pulumi program) can read the header and write
   spans to any trace of the collector. The server cannot scrub the spans.
   Not recommended.
2. **To a relay in the launcher.** For the tool step only, the launcher
   listens for OTLP/HTTP on `127.0.0.1` on a random port, and sets
   `OTEL_EXPORTER_OTLP_ENDPOINT` to it. It checks and bounds the spans
   (section F), masks the secret values of the job as it masks the log
   (ADR 134 decision 8), and sends them to the server through its job
   session. The server checks them again, scrubs them and exports them.
   No collector credential and no collector address reach the job. The
   cost: one receiver in the launcher and one route of the runner
   protocol, which needs a new protocol version (ADR 132).
3. **To a file.** Only for a tool that writes spans to a file: the Pulumi
   CLI with `--otel-traces file://...` writes JSON. The launcher reads the
   file after the step and sends the spans as in 2. Nothing listens on a
   port.
4. **Do nothing.** The job trace has the server and launcher spans only.

**How each tool is turned on.** OpenTofu and Terragrunt read the `OTEL_*`
or `TG_TELEMETRY_*` variables, which the launcher sets for the tool step.
The Pulumi CLI has a flag only (`--otel-traces`), so the step planner of
the Pulumi tool adds the flag with the address of the relay or the file.
Neither value is a credential. A Pulumi CLI older than 3.226.0 has no flag,
and gets A1.4.

**The encoding.** OpenTofu (through `autoexport`) and the Pulumi CLI (over
HTTP) send OTLP as protobuf. So a relay must read protobuf, or the launcher
must ask for JSON: `OTEL_EXPORTER_OTLP_PROTOCOL=http/json`. Whether the
`autoexport` of OpenTofu accepts `http/json` is not checked (question 3).

**Recommendation.** A1.2 for OpenTofu and Terragrunt. A1.3 for the Pulumi
CLI from 3.226.0, because the file is JSON and needs no listener. A1.4 for
a tool or a version with no OTLP (see "When the tool has no OTLP" below).

**A2. The trace context.** The launcher sets `TRACEPARENT` for the tool
step from the span of the step: version `00`, the trace ID of the job, the
span ID of the step, and the flag `sampled` when the job is sampled. A
tool that reads `TRACEPARENT` then makes its root span a child of the step
span. A tool that does not read it makes a new trace. With A1.2 the relay
then rewrites nothing: it drops each span whose trace ID is not the trace
ID of the job (section F). So a tool without `TRACEPARENT` support gives
no tool spans, not a second trace.

**A3. The job record links to the trace.** The job answer of the REST API
gets `traceId` (32 hex characters). The console shows it and, when the
operator sets a link template (for example
`https://traces.example.com/trace/{traceId}`), a link. The template is an
installation setting: an `https` URL with exactly one `{traceId}`, checked
at the boundary. Each state version that a job wrote is found from the job,
so it needs no trace field of its own.

**A4. A state write from a team's own CI (the CI shim).** The shim of the
secrets-out-of-state theme starts the tool. Options:

1. The shim reads a valid `TRACEPARENT` from the CI job, or makes a new
   one, and passes it to the tool. The tool exports to the collector that
   the CI configures. The shim sends the trace ID with its state write in
   a header, and the history entry of the state write keeps it as a
   **client trace ID**. The server never trusts it: it is a link, not a
   parent of a server span.
2. The shim relays spans to the server, as the launcher does in A1.2.
3. Do nothing for the CI shim in v1.

**Recommendation.** A4.1. A4.2 adds a span upload route for a principal
that is not a job session, and so a second untrusted path. It can come
later.

**When the tool has no OTLP.** The job trace still has the job, wait,
phase and step spans, with the duration of each step and its exit code. The
phase span gets the attribute `dustpan.tool_spans` with the value
`unsupported`, `none` (the tool sent none) or `dropped` (the bounds of
section F dropped some), so a missing part of the trace is visible.

### B. Server spans

**What the server traces.**

1. **API requests.** One span for each request of each adapter, from the
   middleware of ADR 32 decision 6: the route pattern (never the path), the
   method, the status, the adapter and the request ID. The same attributes
   as the request log, so ADR 32 decision 7 applies with no new rule.
2. **Jobs.** The spans of section A, built from the times in the job
   record at each transition. The domain package `pkg/job` imports no
   trace code: the server reads the record and builds the spans, as
   ADR 32 decision 9 does for metrics.
3. **Durable operations.** One span for each claim and each step of an
   operation that is not a job phase (for example a restore, a backup or a
   sweep).
4. Store and blob calls. Not in v1: they add many spans and few answers.

**Incoming `traceparent` on an API request.** A client of the API can send
`traceparent`. The options:

- (a) The server ignores it.
- (b) The server span is a child of it.
- (c) The server span stays in its own trace, with a span link to the
  remote context.
- (d) As (c), but the server span is a child when the request
  authenticates with the job token of a live job, and the trace ID is the
  trace ID of that job.

OpenTofu (through `otelhttp`) and the Pulumi CLI (from 3.268.0) send
`traceparent` on their API calls when tracing is on, so with A2 the state
reads and writes of a job carry the trace of the job. Recommendation: (d).
A client cannot then put server spans into a trace of its choice, because
the server checks the trace ID against the job of the credential. Every
other request gets the link of (c).

**The code (CLAUDE.md: ask before a dependency).**

| Option | What it adds | License | Cost |
| --- | --- | --- | --- |
| B1. OpenTelemetry Go SDK | `otel`, `otel/sdk`, `otel/trace`, `otel/metric`, `otlptrace`, `otlptracehttp`, `proto/otlp`, plus gRPC, protobuf, `grpc-gateway`, `genproto` and `backoff` | Apache-2.0 for the OTel modules. The others, as their license files give them: gRPC and `genproto` Apache-2.0, protobuf and `grpc-gateway` BSD-3-Clause, `backoff` MIT. To check with `make licenses`. | The module set grows from 7 direct dependencies, and gRPC is linked even for the HTTP exporter. The growth of the binary is not measured (question 2). Every module in the set is under the age gate of `make lint`, the license check and `govulncheck`. |
| B2. A small exporter of our own: OTLP/HTTP with the JSON encoding, the standard library only | one internal package, likely below 1,000 lines: a span type, a bounded batch queue, the JSON encoding, the HTTP client with TLS | ours | We own the encoding. The precedent is the `otlp` sink of ADR 60 decision 12, which can share the HTTP client and the TLS settings. |
| B3. None. Export only through the collector, which scrapes `/metrics` | nothing | none | No traces at all. Metrics only. |

**Recommendation.** B2. The server needs one exporter, one encoding and one
transport. It needs no sampler plugins, no propagator registry and no
metrics SDK. B1 stays the option if the owner wants gRPC transport or the
OTLP metrics and logs signals from one library later.

### C. Fleet metrics from data we hold

**Cardinality.** A fleet of 10,000 units with a `unit` label on a histogram
of 12 buckets and 4 operations gives 480,000 series from one family. That
is more than a usual Prometheus server keeps for one target. ADR 32
decision 5 already forbids a unit label. So per-unit values go to spans
and to the REST API, never to Prometheus labels.

Allowed Prometheus labels: `tool` (2), `operation` (4), `phase` (4: `plan`,
`apply`, `deliver`, `verify`), `result` (`succeeded`, `failed`,
`canceled`, `expired`), `wait` (`unit-busy`, `frozen`, `approval`). One
family has at most 2 × 4 × 4 × 4 = 128 label sets, times the buckets.

| Metric | Data we hold | Prometheus (no unit label) | Span attribute | REST API (per unit, per collection) |
| --- | --- | --- | --- | --- |
| Plan and apply duration | phase start and end in the job record | `dustpan_job_phase_duration_seconds` histogram | phase span duration | the slowest phases of a namespace or a collection, top N, over a time range |
| Wait for a lock | `queued` with `unit-busy` (a job), and the 409 of a tool lock request (a CI) | `dustpan_job_wait_seconds{wait="unit-busy"}`, `dustpan_lock_conflicts_total{tool}` | wait span | the units with the most wait time |
| Wait for an approval | `awaiting-approval` duration | `dustpan_job_wait_seconds{wait="approval"}` | wait span | per collection |
| Failures | terminal job state | `dustpan_jobs_total{result}` | job span status | failure rate per unit |
| Drift | not held yet: drift detection is RFC 25, a draft | none until RFC 25 | none | none |
| Provider download time and version | not held: the launcher sees one step only. OpenTofu has an "Install Providers" span with `opentofu.provider.address` and `opentofu.provider.version` (see the facts), and the mirror of the supply-chain theme can record it | none in v1 | from the tool spans, if the allowlist keeps them | later, from the mirror |
| Actions invoked | not held: the actions theme records them | none in v1 | none | later, from the job record of that theme |

**Recommendation.** The three columns as in the table. Spans carry the unit
ID and the namespace ID, not the names (question 6).

### D. Change-delivery metrics per collection

These are the four delivery metrics of the DevOps Research and Assessment
program. The theme issue names three. Deployment frequency is a count of
apply jobs and costs nothing more, so the table has it too. This RFC calls
them **change-delivery metrics**. They are values of the REST API
for a collection and a time range. They are not Prometheus families,
because a collection label has no bound.

| Metric | Definition from data we hold | What we lack |
| --- | --- | --- |
| Lead time from merge to apply | the end of the apply phase that wrote a final state version, minus the time of the merge of its commit | the merge time. A job holds the commit, not the merge. Options: the push event of the Git host (RFC 89), or the commit time from the source snapshot. A commit time is set by the author, so it is untrusted and can be wrong. |
| Change failure rate | apply jobs that a person linked to an incident as `cause` (ADR 147 decision 12), divided by all succeeded apply jobs | links only exist where people make them. Option: also count an apply that failed after it wrote state. |
| Time to restore | for an incident linked to a unit of the collection: resolved time minus detection time (ADR 147 decision 1) | nothing for incidents. Option: also the time from a failed apply to the next succeeded apply of the same unit, with no incident. |
| Deployment frequency | succeeded apply jobs, and state writes with no job | nothing |

A state write from a team's own CI has no job and no commit unless the CI
shim sends one. So these metrics cover CI writes only for commit-based
values that the shim sends.

**Recommendation.** Incident-based definitions only, with the fallbacks as
separate, named values. The merge time from the push event of the Git
host when RFC 89 lands. Until then, no lead time (question 7).

### E. Export to the operator's collector

- **Only to the operator's target (ADR 74).** Off by default. No default
  endpoint. Nothing leaves the installation until the operator sets one.
- **Signal:** traces only, over OTLP/HTTP with the JSON encoding (B2).
  Metrics stay on `/metrics` for a collector to scrape.
- **TLS:** `https` is required, except for a loopback address. A plain
  `http` endpoint on another host needs an explicit `insecure` setting,
  and the server logs a warning at start. Options for the CA file and a
  client certificate (mTLS). No redirect is followed. The connection
  checks the resolved IP address against the denied ranges of the egress
  client, unless the operator allows private ranges for this target (a
  collector is often on a private network).
- **Auth:** a header (for example `Authorization: Bearer ...`) from an
  environment variable or a file only (ADR 60 decision 15).
- **Configuration.** Start flags (`--trace-export-endpoint`,
  `--trace-export-ca-file`, `--trace-export-header-file`,
  `--trace-export-sample-ratio`) and their environment variables are the
  static layer. The REST API view `/api/dustpan/v1/settings/trace-export`
  (ADR 212 decision 7) reads and changes the same keys, with
  `installation:read-settings` and `installation:manage-settings`. The
  header is a secret setting of the static layer only: the API answers
  whether it is set, and a `PATCH` of it answers 409. A namespace never
  sets an export target in v1 (question 8).
- **State of the export.** `GET /api/dustpan/v1/settings/trace-export`
  also answers the last error, the time of the last success and the count
  of dropped spans.
- **Sampling.** A ratio from 0 to 1, by trace ID, so a job is kept whole
  or dropped whole. Default 1.

**Recommendation.** As above.

### F. Defense in depth (ADR 250)

Spans of a tool come from code that runs untrusted IaC code, so they are
untrusted input. Attribute values can hold paths and URLs with a
credential in them (see the facts). The code of a job can also send spans
of its own to the relay, with any value in them. A later release of a tool
can add attributes, because the output is not a committed interface.

Each layer below stops the attack alone. Each one has its own test, which
sends hostile input to that layer with the other layers skipped.

1. **Check at the boundary.**
   - The relay, the file reader and the server accept only OTLP, with a
     limit of 1 MiB for each request or file part.
   - The trace ID must be the trace ID of the job. The parent span must be
     in the trace of the job.
   - A span name must be on the allowlist of the tool. Else the span gets
     the name `tool.span`.
   - An attribute must be on the allowlist of keys of the tool, with its
     type and a limit of 256 bytes. The check drops all other attributes
     and all span events.
   - Our own OTel resource replaces the resource of the tool.
   - Test: one test for each rule, and a fuzz test of the reader.
2. **Encode or contain at use.**
   - The exporter writes JSON with an encoder, never by string
     concatenation.
   - A string that goes to a log has no CR and no LF.
   - The console shows span values as text.
   - Test: a hostile string through the exporter and the log.
3. **Limit the effect.**
   - At most 10,000 spans for each job, 2,000 for each step and 32
     attributes for each span.
   - The export queue has a bound. When it is full, it drops the oldest
     batch.
   - An export request has a timeout of 10 seconds.
   - The relay listens on loopback only, and only while the tool step runs.
   - Test: a job at each limit plus one, and a relay request after the step
     ends.
4. **Authorize at use.**
   - The span upload route checks that the job session is live, and that
     the phase and the step belong to the claim. It is the same check as
     the log append.
   - Test: spans of another job, and spans after the end of the claim.
5. **Fail closed and record.**
   - The check drops a span that fails. It never changes the span to make
     it pass.
   - The phase span (`dustpan.tool_spans=dropped`) and the counter
     `dustpan_trace_spans_dropped_total{reason}` hold the drops.
   - A TLS error never falls back to plain text.
   - A change of the export settings writes an audit entry.
   - Test: a bad span gives no export and one count. A TLS failure sends
     nothing.

**Secrets.** Three layers, each one alone enough to stop a secret value that
a step got from its profile: the launcher masks known secret values in each
string (as in the log). The server drops each attribute that is not on the
allowlist. The server masks known secret values again before the export.
A secret that the job made itself (for example a generated password) is not
known to either mask, so the allowlist is the layer that stops it. The
allowlist never holds a key whose value is a resource attribute.

**Telemetry never fails a job.** A failure of the relay, the upload or the
export is logged and counted, and the job goes on. This is fail open for
the job and fail closed for the data: a span that cannot be checked is
dropped, not sent. The audit log gets no entry for each dropped span,
because one bad job could flood it. The counter and the phase attribute
hold the drops.

**Cardinality caps.** Section C: no unit, namespace, user or token label.
A new label value outside the fixed lists goes to `other`.

### G. The first slice

**Persona.** An operator who already runs an OpenTelemetry collector and
wants to see slow API requests of OpenDustpan next to the rest of their
system. The job model has no SQL store on `main` yet, so job traces come
after.

1. The exporter of option B2, with the TLS, auth, bounds and drops of
   sections E and F.
2. One span for each API request of each adapter (item 1 of "What the
   server traces"), and the span link of option (c) for an incoming
   `traceparent`. Option (d) needs job tokens, so it comes with job traces.
3. The start flags and the settings view of section E, with the audit
   entry for a change.
4. The user documentation page.
5. **The e2e step.** The test starts an OTLP/HTTP receiver with TLS and its
   own CA, and starts the server with `--trace-export-endpoint` and
   `--trace-export-ca-file`. It runs `tofu init`, `tofu plan` and
   `tofu apply` through the TFE-compatible API, and `pulumi up` through
   the Pulumi Cloud-compatible API. It checks that the receiver gets one
   span for each request, with the route pattern, the adapter and the
   status. It checks that no canary value of ADR 32 decision 7 (a token, a
   state value, a path) is in any span. It then stops the receiver and runs
   `tofu plan` again: the plan succeeds and the drop counter goes up. An
   endpoint of `http://` on a host that is not loopback refuses to start
   without `insecure`.

Later: job traces from the job record (section A, and item 2 of "What the
server traces"), the launcher relay with its runner protocol version
(A1.2), the Pulumi file (A1.3), the CI shim trace ID (A4.1), the fleet
metrics of C, and the change-delivery metrics of D.

## Open questions

The owner keeps these. Each recommendation is the author's.

1. Accept a tracing feature at all, and so supersede ADR 32 decision 1?
   Recommendation: yes, traces only, export off by default.
2. The code: B1 (OpenTelemetry Go SDK), B2 (our own OTLP/HTTP JSON
   exporter) or B3 (none)? A measure of the binary size with B1 is the
   first step if the owner considers B1. Recommendation: B2, the same choice as the
   `otlp` sink of ADR 60.
3. Spans of the tool: through the launcher relay (A1.2) and the Pulumi
   file (A1.3), with a new runner protocol version and a span upload route,
   or not at all in v1? OpenTofu sends protobuf. If `http/json` does not
   work with its `autoexport`, the relay must read protobuf: a small reader
   of our own for the trace messages only, or `google.golang.org/protobuf`
   in the runner module (ADR 139). Recommendation: the relay after slice
   1, and a check of `http/json` against the pinned OpenTofu in e2e
   first.
4. The Pulumi CLI has `--otel-traces` only from 3.226.0 (March 2026). Which
   is the oldest Pulumi CLI version that gets tool spans, and does an older
   one get step spans only, or a conversion of its `--tracing file:`
   appdash output? Recommendation: 3.226.0 and later only, with no conversion
   of the older format.
5. The attribute allowlist for each tool: who keeps it, and is it a list
   in code (one release to change) or an installation setting?
   Recommendation: a list in code, with a test against the spans of the
   pinned CLI version in e2e.
6. Unit names in span attributes, or unit IDs only? Names help in a trace
   viewer. A name can tell a reader of the collector more than the operator
   wants. Recommendation: IDs by default, names behind a setting.
7. Lead time: the merge time from the push event of the Git host only, or
   also the commit time as a fallback? Recommendation: the push event only.
8. Trace export for each namespace (a tenant of a hosted installation sends
   its own job traces to its own collector)? Recommendation: not in v1. The
   installation target only.
9. An incoming `traceparent` on an API request: ignored (a), a parent (b),
   a link (c), or a parent only for the job token of the same job (d)?
   Recommendation: (d). Whether OpenTofu sends `traceparent` to a remote
   backend through `otelhttp` is likely, but not checked.
10. Sampling: a ratio by trace ID, or always on for failed jobs too (tail
    sampling)? Recommendation: a ratio only. Tail sampling belongs to the
    collector.
11. The bounds of section F (10,000 spans for each job, 2,000 for each
    step, 1 MiB for each request): do these hold for a state of 10,000
    resources? We found no span for each resource in the OpenTofu source,
    but each provider call and each HTTP request may have one, and the
    Pulumi CLI also gets spans from each provider plugin. Recommendation:
    measure in e2e with a fixture of 10,000 resources before the ADR.

## Proposed glossary lines

- **Job trace**: the trace of one job. The server makes its trace ID at the
  start of the job and keeps it in the job record. The job is the root
  span. Waits, phases, steps and the spans of the tool are its children.
  _Avoid_: run trace.
- **Trace export**: the OTLP/HTTP export of spans to a collector that the
  operator configures. Off by default. _Avoid_: telemetry (ADR 74 uses it
  for data that goes to a party that the operator did not configure).
- **Change-delivery metrics**: lead time from merge to apply, change
  failure rate, time to restore and deployment frequency, for a collection
  and a time range. _Avoid_: DORA metrics (ADR 147 uses DORA for the EU
  regulation).
- **Client trace ID**: a trace ID that a client sent with a state write. It
  is a link only, never trusted.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
