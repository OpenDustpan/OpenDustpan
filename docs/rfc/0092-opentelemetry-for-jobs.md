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
- **RFC 34** (accepted by ADR 32) left tracing open. This RFC takes over
  that question. When an ADR accepts this RFC, it also closes the tracing
  item of RFC 34 (the text "Tracing stays open"). If the owner drops this
  RFC, tracing stays open in RFC 34.
- **RFC 46 and ADR 147 decision 12:** RFC 46 (insights) is a stub and owns
  the dashboards and the delivery metrics. ADR 147 decision 12 made the
  incident links so that RFC 46 can read them. This RFC defines only the
  data and the REST values of the change-delivery metrics (section D). RFC
  46 owns how they look and the reports. If RFC 46 changes a definition, it
  changes this section by a new RFC, not in parallel.
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
  that accepts this RFC supersedes that sentence. The same ADR adds the
  `other` value to the label rule of ADR 32 decision 5 (section F, cardinality
  caps). It also closes the tracing item of RFC 34.
- ADR 134 decision 6 and ADR 101 decision 7: the **reserved variable set**.
  A step variable fails the step (the server at start, and the launcher
  again) when its name, compared with the case ignored, starts with
  `OTEL_`, `TG_TELEMETRY_`, `TERRAGRUNT_TELEMETRY_` or `PULUMI_OTEL_`, or is
  `TRACEPARENT` or `TRACESTATE`. The rule holds whether or not the launcher
  sets the variable for this step. So a step variable cannot override a
  value of the launcher. This is the only effect of the set. Job code can
  still export `OTEL_*` variables in its own process, pass
  `--otel-traces` or `--tracing` to a tool, or run its own exporter. The
  only control on that is egress (ADR 137 decision 6, and the egress proxy
  of RFC 16). This RFC adds no new path out of the job: the relay listens
  on loopback and sends only through the job session. ADR 101 decision 7 lists the rule, and ADR 134
  decision 6 says that the launcher checks it again. Both ADRs change.
- ADR 101 decision 7: `OTEL_EXPORTER_OTLP_HEADERS` and
  `OTEL_EXPORTER_OTLP_TRACES_HEADERS` can hold an authorization header.
  The reserved set above covers them, and `job.IsCredentialEnv` also gets
  them as names, so a code path that uses only that rule stays safe.
- ADR 134 decision 1 and ADR 139: only if the owner picks the second
  option of F1 (a protobuf library in the launcher). The launcher then
  links a module outside the standard library, which decision 1 forbids.
  The recommended option (a reader of our own) needs no amendment.
- ADR 131 decisions 3, 4, 12 and 14. The session path table (decision 3)
  gets the span route of A1.2. The port table (decision 4) gets the port
  method `AppendStepSpans`. The caps and the rate budget (decision 14) get
  the span limits and the span budget of A1.2 and F3. Decision 12 needs a
  note, because the span route does not stop the claim on `conflict` or
  `invalid` (A1.2, "Errors on the span route").
- ADR 132 decisions 5 and 7: **no amendment.** A new path inside
  `/runner/v1` is an addition, and an ignorable field in the assignment
  ("trace this step") does not change what a step does: it only adds
  telemetry variables and a listener, and the commands, the credentials
  and the result of the step stay the same. So this RFC needs
  no `/runner/v2` and no new assignment version. A launcher that does not
  know the field ignores it, and gets no tool spans (A1.4). It also skips
  the second check of the reserved set, so only the check at the start
  applies to it.

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
- **Protocol.** `autoexport` accepts `grpc` and `http/protobuf` for
  `OTEL_EXPORTER_OTLP_PROTOCOL`. It does **not** accept `http/json`
  (checked at the released tags of `autoexport` that OpenTofu uses). So
  OpenTofu always sends protobuf.
- **Backend API calls.** OpenTofu does **not** send `traceparent` on the
  calls to a state backend. The `cloud` and `remote` backends use go-tfe,
  which has no OpenTelemetry. The `http` backend uses a plain
  `retryablehttp` client. Only `internal/httpclient` (the registry and the
  provider downloads) wraps `otelhttp`.
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
  No environment variable turns it on: it is a flag only. The `file://`
  scheme writes OTLP JSON.
- **`TRACEPARENT`.** From 3.229.0: "Allow TRACEPARENT to be set for CLI
  invocations, parenting the pulumi spans under an existing parent span".
  The CLI reads it only with `--otel-traces`. It does not read
  `TRACESTATE`. From 3.268.0 the CLI sends the W3C `traceparent` header on
  its API calls when `--otel-traces` is set.
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
- **Version.** OpenTelemetry first came in v0.55.5, with the variables
  `TERRAGRUNT_TELEMETRY_*`. The names `TG_TELEMETRY_*` came later and are
  the current ones. The latest tag is v1.1.6.
- **Sensitive data.** The docs give no warning. The examples show command
  arguments and directory paths as attributes.

### OpenTelemetry Go and the protocols

- `go.opentelemetry.io/otel` is under the Apache License 2.0. The latest
  tag is v1.47.0. The go.mod of `otlptracehttp` requires, beside the OTel
  modules, `go.opentelemetry.io/proto/otlp`, `google.golang.org/grpc`,
  `google.golang.org/protobuf`, `cenkalti/backoff/v5` and `go-logr/logr`.
  Indirect: `grpc-gateway/v2`, `genproto` (`googleapis/api`,
  `googleapis/rpc`), `x/net` and others. Code that is not a test links
  gRPC through `internal/otlpconfig`. Version 1.46.0 of `otlptracehttp`
  added the JSON encoding (`WithEncoding`, `EncodingJSON`). It still links
  gRPC through `internal/otlpconfig`, and it still needs protobuf, so the
  JSON encoding does not remove the dependency set.
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
job can join their spans to its trace (option A2). The Pulumi CLI reads it
only with `--otel-traces`, and no tool needs `TRACESTATE`. OpenTofu always
sends protobuf (`http/json` is not available), and the Pulumi CLI sends
protobuf over HTTP. So a relay that receives their spans must read
protobuf (question 3). The Pulumi CLI can also write OTLP JSON files, so a
file path (option A1.3) works for it. OpenTofu sends no `traceparent` to a
backend, so option (d) of section B helps the Pulumi CLI from 3.268.0
only. No tool documents which attributes can hold sensitive values, and
the OpenTofu output is not a committed interface. So the attribute
allowlist must be tested against each pinned CLI version.

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
   cost: one receiver in the launcher and one new path in the runner
   protocol. Per ADR 132 decisions 5 and 7, a new path inside `/runner/v1`
   and an ignorable "trace this step" field of the assignment need no new
   protocol version and no new assignment version. A launcher that does
   not know the field sends no spans. The new route is
   `PUT /runner/v1/session/steps/{step}/spans/{seq}`, with a body of at
   most 1 MiB and the header `Dustpan-Content-SHA256`, like a log chunk
   (ADR 131 decision 10). Its object is the step of the live claim, and
   the session key of that claim authorizes it. The route declares the
   same action and the same object as the log chunk route: the credential
   action of the session (for example `runner:claim`) on the step of the
   live claim. A runner key alone cannot call it. The route test
   (CLAUDE.md) finds the action on the route, and the route is not on the
   public list. The route calls one port of the job domain, for example
   `AppendStepSpans(claim, step, seq, sha256, body)`, which needs
   conformance tests before an adapter implements it. A retry with the
   same `seq` and the same hash changes nothing and answers 204. The same
   `seq` with another hash fails with `conflict`. The limits are in the
   style of ADR 131 decision 14. One step has at most 2,000 spans, 256
   requests and 8 MiB. One job has at most 10,000 spans, 1,024 requests
   and 32 MiB. The job is the unit of this budget, not the phase. This is a
   proposal for the owner. A request above a cap fails with `invalid` and
   changes nothing.

   The span uploads of a session have their own budget of 10 requests each
   second. This value is a proposal for the owner. The budget sits inside
   the 50 requests each second of ADR 131 decision 14. The server answers
   `rate-limited` to a span upload above 10 requests each second, and to a
   session above 50 requests each second. The lower budget keeps a burst of
   spans from using the requests of the log chunks, the renewals and the
   finish.
3. **To a file.** Only for a tool that writes spans to a file: the Pulumi
   CLI with `--otel-traces file://...` writes OTLP JSON. The launcher
   reads the file after the step and sends the spans as in 2. Nothing
   listens on a port. The reader is a security reader (section F):
   - The file is in `spans/` under the scratch directory of the step, on
     the disk of the runner. It is not in the memory-backed volume of the
     secrets. That directory has a quota of 64 MiB. The launcher also passes a
     limit that stops the growth of the file. Each layer has its own test: a
     file that grows past 64 MiB with the quota in place, and the same file
     with the quota removed in the test, where the limit of the launcher
     must stop the growth. If the tool cannot
     write the file (the path is not writable, or the quota is full), the
     step still succeeds and gets no tool spans.
   - The launcher opens the file with `os.Root`, never by a path string
     that the step could replace with a link.
   - It opens the file with `O_NONBLOCK`. An open of a FIFO with no writer
     returns at once. It does not wait for a writer.
   - It calls `fstat` on the open file, and it requires a regular file. A
     FIFO, a device, a socket or a directory gets no tool spans.
   - It sets one deadline for the whole read, of 10 seconds. This value is a
     proposal for the owner. It is the export timeout of F3. A read that
     reaches the deadline stops, and the step is not affected.
   - Test: one test for each of the three checks. The test of the open
     starts a FIFO with no writer, and it checks that the open returns at
     once. The test of `fstat` gives a FIFO with a writer, and it checks
     that no span is read. The test of the deadline writes a regular file
     slowly, so that the read reaches the deadline before the size limit,
     and it checks that the read stops.
   - A size limit of 64 MiB for the file. The launcher checks it while it
     reads, not from the file size alone. A file over the limit is read
     up to the limit, the rest is dropped and counted, and the step is not
     affected.
   - It reads a stream of JSON documents (the CLI appends one document for
     each export), one at a time, with a limit for each document of 1 MiB.
     It never reads the whole file into memory.
   - Each document goes through the same checks as a request of the relay.
4. **Do nothing.** The job trace has the server and launcher spans only.

**Source of a span.** Every span from a tool gets the attribute
`dustpan.span.source=tool`. Spans that the server or the launcher makes
get `server` or `launcher`. No metric, no gate, no inventory value and no
decision (an approval, a freeze or a policy fact) is ever derived from a
tool span. The tool is untrusted, and its output is not a committed
interface. The console may show tool spans as text. Section C uses the
job record only.

**Telemetry never fails the job.** The launcher sets only `OTEL_*`,
`TG_TELEMETRY_*` and `TRACEPARENT` values that the tool accepts, and the
step planner of the Pulumi tool adds only a flag value that the pinned CLI
accepts. A value that a tool rejects at start could fail the step, so the
step planner has a table of the accepted values for each pinned version,
and a test for it. An OpenTelemetry exporter in OpenTofu and in the Pulumi
CLI exports in the background: an endpoint that is not reachable gives a
log line or an ignored error, not an exit. The first slice does not rely on
this reading of the code: the e2e step (G) runs `plan` and `apply` with
an endpoint that nobody listens on, and with a relay that has stopped, and
both must succeed. If a pinned release fails this test, that release gets
A1.4 (no tool spans).

**How each tool is turned on.** OpenTofu and Terragrunt read the `OTEL_*`
or `TG_TELEMETRY_*` variables, which the launcher sets for the tool step.
The Pulumi CLI has a flag only (`--otel-traces`), so the step planner of
the Pulumi tool adds the flag with the address of the relay or the file.
Neither value is a credential. A Pulumi CLI older than 3.226.0 has no flag,
and gets A1.4. The launcher sets `TRACEPARENT` and never `TRACESTATE`,
because no tool needs it.

**The encoding.** OpenTofu (through `autoexport`, which accepts only
`grpc` and `http/protobuf`) and the Pulumi CLI (over HTTP) send OTLP as
protobuf. `http/json` does not work with OpenTofu. So the relay must read
protobuf. The launcher stays on the standard library (ADR 134 decision 1,
ADR 139), so there are two ways to read it:

1. **A reader of our own** for the trace messages only
   (`ExportTraceServiceRequest` down to the span, its attributes and its
   status). It is small, because the launcher needs about ten message types
   and the wire format has five field types. It has limits that the
   generic library does not have (F). It has fuzz tests. Recommended.
2. **`google.golang.org/protobuf`** in the launcher. The launcher then
   links a module outside the standard library. This amends ADR 134
   decision 1 and the module boundary of ADR 139 (see "ADRs this RFC would
   amend"). It is a mature parser, but its allocations are not bounded by
   our limits, and it adds to the attack surface of the one binary that
   runs next to untrusted code.

**Recommendation.** Option 1, with fuzz tests (F1).

**Recommendation.** A1.2 for OpenTofu and Terragrunt. A1.3 for the Pulumi
CLI from 3.226.0, because the file is JSON and needs no listener. Both come
after the first slice (G). A1.4 for
a tool or a version with no OTLP (see "When the tool has no OTLP" below).

**Errors on the span route.** The launcher treats each error of the span
route as a drop and counts it (section F). The exceptions are `claim-lost`
and `canceled`. A `claim-lost` stops the step at once. A `canceled` stops
the step with its grace period. So the span route does not stop the claim
on `conflict` or `invalid`, as ADR 131 decision 12 does for the other
routes. It also does not wait on `rate-limited` or `unavailable`. Test: one
test for each code of the span route. The step continues after each code,
except `claim-lost` and `canceled`, and the drop counter goes up by one.

**The relay has no authentication.** The step is the only process that
can reach it, and a request with a token would put the token in a
variable of the step. This is acceptable for four reasons:

- The relay binds the literal address `127.0.0.1:0` (never `localhost`
  and never `0.0.0.0`), and it listens only while the tool step runs.
- On the machine coordinator, each sandbox has its own network namespace
  (ADR 137 decision 6), so loopback reaches only that sandbox.
- A process that can reach the relay runs next to the tool, with the same
  trust as the step. All it can do is send spans, which pass the checks of
  section F, and which the job could send anyway.
- The relay does not check the user of the peer. `SO_PEERCRED` works only
  on Unix sockets, and the relay listens on TCP. The tool also runs as a
  separate step user (ADR 134 decision 11), so a same-user check would drop
  the spans of the tool. No layer is listed that cannot run. The checks of
  section F apply to each request, whoever sends it.

**The Kubernetes case.** In a pod, a sidecar container shares the network
namespace of the pod. A sidecar (for example a service mesh proxy, or an
agent) can then reach the relay, and so can any other container of the
pod. No check of the peer user stops a sidecar. The accepted risk is the same:
a sidecar can send spans, and the checks of section F apply. The pool
documentation says this. An operator who cannot accept it uses A1.4 for
that pool, or A1.3 (the file) for the Pulumi CLI.

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
   parent of a server span. The server checks the header at the boundary.
   The value must be 32 lowercase hex characters, and it must not be all
   zeros. A value that fails the check is rejected. The state write then
   answers `invalid`, and nothing is stored. Test: one test of the check. It
   sends values of 31 and of 33 characters, uppercase hex, a character that
   is not hex, the all-zero ID, and a value with CR or LF. Each value gets
   `invalid`, and no history entry is written.
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

**Several instances, and a crash.** The job record holds the trace ID,
the times of each transition, and nothing else. Any instance can build a
span from it. The instance that makes a transition exports the span of
that transition (a phase, a wait) at that time. The instance that ends the
job exports the root span, with the start time from the record. So
delivery is **at most once**: an instance that crashes before it exports
loses the spans in its queue, and the spans are not rebuilt later. Until
the root span ends, the child spans in the collector have a parent that the
collector has not seen. A trace viewer shows them as orphans, which is
right for a job that is still running. A job whose last instance crashes
never gets a root span. This is accepted for v1: the collector is
observability, not a record, and the job record holds the truth. A later
design can add an outbox table if the owner wants at-least-once delivery.

**Incoming `traceparent` on an API request.** A client of the API can send
`traceparent`. The options:

- (a) The server ignores it.
- (b) The server span is a child of it.
- (c) The server span stays in its own trace, with a span link to the
  remote context.
- (d) As (c), but the server span is a child when the request
  authenticates with the job token of a live job, and the trace ID is the
  trace ID of that job.

Only the Pulumi CLI (from 3.268.0, with `--otel-traces`) sends
`traceparent` on its API calls. OpenTofu sends none to a state backend
(see the facts), so (d) helps the Pulumi CLI only. For OpenTofu, the state
writes of a job join the job trace through the phase span: the server sets
the parent of the state-write span from the job of the job token, not from
a header. Recommendation: (d). A client cannot then put server spans into
a trace of its choice, because the server checks the trace ID against the
job of the credential. Every other request gets the link of (c).

**The code (CLAUDE.md: ask before a dependency).**

| Option | What it adds | License | Cost |
| --- | --- | --- | --- |
| B1. OpenTelemetry Go SDK | `otel`, `otel/sdk`, `otel/trace`, `otel/metric`, `otlptrace`, `otlptracehttp` (from 1.46.0 it has the JSON encoding, and it still links gRPC through `internal/otlpconfig`), `proto/otlp`, plus gRPC, protobuf, `grpc-gateway`, `genproto` and `backoff` | Apache-2.0 for the OTel modules. The others, as their license files give them: gRPC and `genproto` Apache-2.0, protobuf and `grpc-gateway` BSD-3-Clause, `backoff` MIT. To check with `make licenses`. | The module set grows from 7 direct dependencies, and gRPC is linked even for the HTTP exporter. The growth of the binary is not measured (question 2). Every module in the set is under the age gate of `make lint`, the license check and `govulncheck`. |
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
| Lead time from merge to apply | the end of the apply phase that wrote a final state version, minus the time of the merge of its commit | the merge time. A job holds the commit, not the merge. Options: the push event of the Git host (RFC 89, accepted by ADR 271, where push events are not yet returned), or the commit time from the source snapshot. A commit time is set by the author, so it is untrusted and can be wrong. |
| Change failure rate | apply jobs that a person linked to an incident as `cause` (ADR 147 decision 12), divided by all succeeded apply jobs | links only exist where people make them. Option: also count an apply that failed after it wrote state. |
| Time to restore | for an incident linked to a unit of the collection: resolved time minus detection time (ADR 147 decision 1) | nothing for incidents. Option: also the time from a failed apply to the next succeeded apply of the same unit, with no incident. |
| Deployment frequency | succeeded apply jobs, and state writes with no job | nothing |

A state write from a team's own CI has no job and no commit unless the CI
shim sends one. So these metrics cover CI writes only for commit-based
values that the shim sends.

**Recommendation.** Incident-based definitions only, with the fallbacks as
separate, named values. The merge time from the push event of the Git
host, when a push event is recorded. Until then, no lead time (question 7).
RFC 46 owns how these values look.

### E. Export to the operator's collector

- **Only to the operator's target (ADR 74).** Off by default. No default
  endpoint. Nothing leaves the installation until the operator sets one.
- **Signal:** traces only, over OTLP/HTTP with the JSON encoding (B2).
  Metrics stay on `/metrics` for a collector to scrape.
- **TLS:** `https` is required, except for a loopback address. A plain
  `http` endpoint on another host needs an explicit `insecure` setting,
  and the server logs a warning at start. `insecure` and "allow private
  ranges" reduce security. Recommendation: both are static-only (start
  flag or environment variable), and a `PATCH` of them answers 409. The
  other option: the API may set them, with a fresh MFA step (ADR 212
  decision 3), and the whole `PATCH` needs the step. Options for the CA file and a
  client certificate (mTLS). No redirect is followed. The connection
  checks the resolved IP address against the denied ranges of the egress
  client, unless the operator allows private ranges for this target (a
  collector is often on a private network).
- **Auth:** a header (for example `Authorization: Bearer ...`) from an
  environment variable or a file only (ADR 60 decision 15).
- **The endpoint and its header are one unit.** If the API could change
  the endpoint and keep the static header, an administrator with
  `installation:manage-settings` could set the endpoint to a host they
  control, and the server would send the static header (a token for the
  real collector) to that host. So: **when a static header is set, the
  endpoint is also static-only**, and a `PATCH` of it answers 409. When no
  header is set, the endpoint may change by the API. As a second layer, the
  server code never attaches a static header to an endpoint that came from
  the database, whatever the order of the layers. Test: a `PATCH` of the
  endpoint with a static header set answers 409 and changes nothing, and a
  unit test of the exporter builder gives no header for an endpoint from
  the database even if the header value is set.
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
- **Span storage.** This is a proposal: a memory queue. The server holds the
  accepted spans in the bounded export queue of section F. It writes them to
  no table and no blob. A restart drops the spans in the queue, as section B
  describes for the delivery of at most once. When export is off, the server
  drops each span at once and counts it. The other option is a blob for each
  job. Its retention period is set by the owner, and it has an entry in the
  table list of the backup domain (`pkg/backup`), so that a backup holds the
  spans. This option keeps the spans across a restart. It adds a second store
  for data that is only observability.

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
     limit of 1 MiB for each request or file part. This limit is on the
     **decompressed** bytes, counted while the data is read, not from a
     header. The relay accepts the content encodings `identity` and `gzip`
     only, and answers 415 to others. The server route does the same.
   - The protobuf reader checks the depth of nesting (at most 8 levels),
     the count of repeated fields (at most 1,024 spans for each request,
     and the 32 attributes for each span) and each length against the
     bytes that remain, **before** it allocates. A field that is not in the
     trace messages is skipped by its length, never stored.
   - The trace ID must be the trace ID of the job.
   - The parent of a tool span must be the step span or a tool span of the
     same step. Other parents drop the span.
   - A span ID that equals a span ID of the server or of the launcher, or
     that repeats a span ID already received for the job, drops the span.
     The set of received span IDs of a job is in the store, not in the
     memory of one instance, so each instance checks the same set. The write
     of a span ID runs in the same transaction as the span, with the
     namespace fence that every write in a namespace carries.
   - A span with a start or end time outside the window of the step (with
     a tolerance of 5 seconds) drops the span. A span that arrives before
     the step ends has no known end of the step. The server checks its start
     time against the start of the step. It checks its end time against the
     time of arrival plus the tolerance. The server does not export a span of
     a running step. When the step ends, it checks each held span against the
     end of the step, with the same tolerance, and it drops each span that
     ends after that point.
   - No key that starts with `dustpan.` is ever on the allowlist of a
     tool. The server sets `dustpan.span.source` after the filter, so a
     tool cannot forge it.
   - A span name must be on the allowlist of the tool. Else the span gets
     the name `tool.span`.
   - An attribute must be on the allowlist of keys of the tool, with its
     type and a limit of 256 bytes. The check drops all other attributes
     and all span events.
   - The check keeps the status code of a span (`unset`, `ok` or `error`).
     It drops the status message. It drops every link and the `trace_state`.
     It drops the `dropped_*` counts of attributes, events and links.
   - The check keeps the kind of a span only when it is one of the six kinds
     of OpenTelemetry: `unspecified`, `internal`, `server`, `client`,
     `producer` or `consumer`. A span with another kind fails the check and
     is dropped.
   - Test: one test sends a span with a status message, a link, a
     `trace_state`, a `dropped_*` count and a kind outside the six. The
     exported span has the status code. It has no status message, link,
     `trace_state` or `dropped_*` count. The span with the other kind is
     dropped.
   - Our own OTel resource replaces the resource of the tool.
   - Test: one test for each rule. For the rules above: a span with the ID
     of a server span, a repeated span ID, a parent in another step, a
     parent that is a server span, a time before the step starts and after
     it ends, a `dustpan.span.source=server` attribute from a tool, and a
     `dustpan.` key of any other name, a repeated span ID that reaches a second
     instance, a span that ends after the step ends, and a span with a kind
     outside the six. And a fuzz test of the whole path:
     decompress, then parse, then check. The fuzz test includes a gzip
     bomb, a deep nest and a huge repeated count.
2. **Encode or contain at use.**
   - The exporter writes JSON with an encoder, never by string
     concatenation.
   - A string that goes to a log has no CR and no LF.
   - The console shows span values as text.
   - Test: a hostile string through the exporter and the log.
3. **Limit the effect.**
   - At most 10,000 spans for each job, 2,000 for each step and 32
     attributes for each span.
   - A total budget of 32 MiB of span bytes for each job, and the rate
     limits of A1.2: 10 span requests for each second for each session,
     inside the 50 requests for each second of the session. Over a limit:
     drop and count.
   - The export queue has a bound. When it is full, it drops the oldest
     batch.
   - An export request has a timeout of 10 seconds.
   - The relay listens on loopback only, and only while the tool step runs.
   - Test: a job at each limit plus one, a job over the byte budget and
     the rate limit, and a relay request after the step ends.
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
export is logged and counted, and the job goes on. A tool that gets an
endpoint that is not reachable must not exit with an error (see A1, "Telemetry
never fails the job"), and the e2e step of the first slice proves it. This is fail open for
the job and fail closed for the data: a span that cannot be checked is
dropped, not sent. The audit log gets no entry for each dropped span,
because one bad job could flood it. The counter and the phase attribute
hold the drops.

**Cardinality caps.** Section C: no unit, namespace, user or token label.
A new label value outside the fixed lists goes to `other`. ADR 32 has no
`other` bucket today, so this rule is new, and an ADR that accepts this RFC
adds it to ADR 32.

**The number of families.** ADR 32 decision 2 says that a new ADR can
change the format when the set grows past about 15 families. `main` already
has about 25. Section C adds five (`dustpan_job_phase_duration_seconds`,
`dustpan_job_wait_seconds`, `dustpan_jobs_total`,
`dustpan_lock_conflicts_total` and `dustpan_trace_spans_dropped_total`).
So the count passes the line of ADR 32 for a second time, and the ADR that
accepts this RFC says that the hand-written text format stays for the
count of about 30 (or moves to a library: another dependency question,
which this RFC does not ask).

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
   `tofu plan` again: the plan succeeds and the drop counter goes up. It
   also starts the server with an endpoint that nobody listens on, and runs
   `tofu plan`, `tofu apply` and `pulumi up`: all succeed. An endpoint of
   `http://` on a host that is not loopback refuses to start without
   `insecure`. When the relay comes (A1.2), the same step runs with a relay
   that has stopped and with a bad `OTEL_EXPORTER_OTLP_ENDPOINT`, and plan
   and apply still succeed. When the Pulumi file comes (A1.3), the same step
   also runs with a spans file that cannot be written, and with a spans file
   over its size limit. The step succeeds in both cases, with no tool spans.

Later: job traces from the job record (section A, and item 2 of "What the
server traces"), the launcher relay with its new path in `/runner/v1`
(A1.2), the Pulumi file (A1.3), the CI shim trace ID (A4.1), the fleet
metrics of C, and the change-delivery metrics of D.

## Open questions

The owner keeps these. Each recommendation is the author's.

1. Accept a tracing feature at all, and so supersede ADR 32 decision 1?
   Recommendation: yes, traces only, export off by default.
2. The code: B1 (OpenTelemetry Go SDK), B2 (our own OTLP/HTTP JSON
   exporter) or B3 (none)? A measure of the binary size with B1 is the
   first step if the owner considers B1. Recommendation: B2, and so
   supersede ADR 32 decision 1, with a stdlib exporter as in ADR 60
   decision 12.
3. Spans of the tool: through the launcher relay (A1.2) and the Pulumi
   file (A1.3), with a new span upload path in `/runner/v1` (no new
   protocol version), or not at all in v1? OpenTofu sends protobuf only
   (`http/json` does not work with its `autoexport`), so the relay must
   read protobuf: a small reader of our own for the trace messages, or
   `google.golang.org/protobuf` in the launcher, which amends ADR 134.
   Recommendation: tool spans never go from the job straight to the
   collector. The Pulumi CLI uses the file (A1.3). OpenTofu and Terragrunt
   use the relay (A1.2) with a protobuf reader of our own and fuzz tests.
   Both come after slice 1.
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
   No lead time is shown until a push event is recorded (RFC 89 is accepted
   by ADR 271, but its push events are not yet returned).
8. Trace export for each namespace (a tenant of a hosted installation sends
   its own job traces to its own collector)? Recommendation: not in v1. The
   installation target only.
9. An incoming `traceparent` on an API request: ignored (a), a parent (b),
   a link (c), or a parent only for the job token of the same job (d)?
   Recommendation: (d). OpenTofu sends no `traceparent` to a state backend,
   so (d) helps the Pulumi CLI from 3.268.0 only. The server sets the
   parent of the state-write span of an OpenTofu job from its job token.
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
