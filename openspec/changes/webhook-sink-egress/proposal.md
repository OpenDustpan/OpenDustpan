Issue: #2984.

## Problem

**Persona: an operator who sends the audit log to a SIEM.** The operator sets
`--audit-sink webhook:https://siem.example/hooks/dustpan`, an `otlp:` item, or
the same kind of item for the application log (`--log-sink`). The server sends
each batch through a bare HTTP client, so the egress rules do not check the URL,
the resolved address of each connection, or the metadata addresses. A DNS
answer that changes after the operator sets the URL can send the audit log to
an internal address or to a cloud metadata address, and the delivery counts as
a success: the operator sees a healthy sink while the wrong host holds the
entries, and only a network rule outside the server stops the delivery.

## Evidence

Each line is from the current checkout. Open each file at the line.

- `cmd/opendustpan/auditsinks.go:336`: `s, err := webhook.New(webhook.Config{Client: nil, URL: rawURL, Secret: secret, Timeout: 0})` in `openWebhookSink` (line 330). The report says "near line 333"; the line is 336.
- `cmd/opendustpan/auditsinks.go:355`: `Client: nil, Headers: headers, URL: rawURL, ServiceName: "", Timeout: 0,` in `openOTLPSink` (line 348). The imports at lines 20 to 27 hold `pkg/logsink/webhook` and `pkg/logsink/otlp`, so these are the audit sink adapters, not the notification sender.
- `pkg/logsink/webhook/webhook.go:119-128`: `if cfg.Client != nil { client = *cfg.Client } else { ... client.Timeout = timeout }`. A nil client gives a client with a timeout only.
- `pkg/logsink/otlp/otlp.go:243-252`: the same code for the OTLP sink.
- `pkg/logsink/webhook/webhook.go:19-35` and `pkg/logsink/otlp/otlp.go:31-...`: neither package imports `egresshttp`. The search `rg egresshttp pkg/logsink` finds no line.
- `cmd/opendustpan/logsinks.go:127`: `sink, _, err := openSink(named.spec, sinkConfig{...})` in `openSinkHandler` (line 112). The log sinks use the same two adapters.
- `docs/guides/audit-log.md:802`: "The server sends to any host that it can reach, internal addresses and cloud metadata endpoints included." Line 825 says the same for the OTLP sink. The guide states the current behavior, so the report is true on the checkout.
- Other outbound clients that do not use `egresshttp`:
  - `internal/oauth/client.go:255`: `next = http.DefaultTransport`; `cmd/opendustpan/signin.go:69` and `:94` pass `HTTPClient: nil`. `internal/cli/client/health.go:154`, `cmd/dustpan-launcher/transport.go:59`, `cmd/demo-seed/main.go:266`, `cmd/s3bench/s3raw.go:45`.
  - `internal/headerauth/keys.go:75`: no caller in the server. Test helpers: `internal/servertest/servertest.go:145`, `internal/clientgen/conformance/proxy.go:76`.

The report holds for the two sinks; its line number is off by three lines.

## Fix boundary

IN:

- `cmd/opendustpan/auditsinks.go`: `sinkConfig` carries the egress rules and the
  client options; `openWebhookSink` and `openOTLPSink` run
  `egress.CheckURL(rawURL, egress.HTTPSOnly)` and build
  `egresshttp.ClientWith(rules, egress.NoRedirect, egress.HTTPSOnly, options)`
  for `Client`; `openSink` and `openAuditSinks` pass them.
- `cmd/opendustpan/logsinks.go`: `openSinkHandler` passes the same rules and
  options.
- Each repeated check carries a comment that names the layers that it does not
  rely on (the start check, the URL check of the client, the dial guard).
- `e2e/sinks.go`, `e2e/audit_sinks_test.go`: the delivery steps use the
  in-process server with `dustpan.WithAuditSinks` and a sink client whose rules
  allow loopback for the test, as the chat sender does. The binary step proves
  that a loopback sink URL fails the start.
- `docs/guides/audit-log.md`: the Webhook and OTLP sections state the egress
  rules, the refusal, and the sink status that shows it.
- A static setting for the allowed private ranges of the audit and log sinks
  (`--audit-sink-allowed-ranges`, the matching `DUSTPAN_*` variable and file
  key), parsed by `egress.ParseAllowedRanges`. Empty by default: no private
  range. It follows the same pattern as the LDAP and Git host allowed ranges.
  An invalid or too wide range stops the start and names the setting. The
  effective value shows in the settings API. The guide gives the setting for
  a receiver on a private network.

OUT:

- The sign-in clients (`internal/oauth`, `pkg/dustpan.WithSignInHTTPClient`,
  `cmd/opendustpan/signin.go`): a decision already gives them the egress client;
  that wiring is its own change.
- The CLI clients (`internal/cli/client/`): they call the server that the user
  names. `cmd/dustpan-launcher`, `cmd/demo-seed` and `cmd/s3bench` are a client,
  a dev tool and a benchmark.
- `internal/headerauth`: no caller in the server.
- `internal/servertest` and `internal/clientgen/conformance`: test helpers.
- The syslog sink: it is not HTTP. The same check for it is its own change.
- An allowed-range setting for a sink: after this change the operator cannot
  reach a receiver on a private network, as with the built-in notification
  senders. A setting for it is its own change.
- The OTLP `?ca-file=` option.

## Check

The fix is security code. Each layer has its own test with hostile input, and
each test fails when its guard goes.

| Layer | Test | Mutation that fails it |
| --- | --- | --- |
| The URL at the boundary: `egress.CheckURL` at the start | `TestAuditSinksRefuseBlockedURLAtStart` (`webhook:https://169.254.169.254/hook` fails the start and names the item) | remove the `egress.CheckURL` call from `openWebhookSink` |
| The URL at each request: the client checks the URL of the request and of each redirect | `TestWebhookSinkRefusesBlockedURL` (a URL with a loopback literal fails with `egress.ErrRefused` and no dial refusal) | remove the `checkURL` transport of `egresshttp.ClientWith` |
| The resolved address at connect time: the dial guard | `TestWebhookSinkRefusesBlockedAddress` (a host that resolves to a loopback address fails with `egresshttp.ErrDialRefused`, and the receiver reads no request) | pass `Client: nil` |
| The effect limit: `egress.Timeout` on each request | `TestWebhookSinkStopsASlowReceiver` | remove `Timeout` from `egresshttp.ClientWith` |
| Fail closed: the refused round is a failure, and the receiver holds nothing | `TestWebhookSinkRefusedAddressGetsNothing` (the receiver read no request, and the sink status holds the refusal) | count the refused round as delivered |
| The OTLP sink takes the same client | `TestOTLPSinkRefusesBlockedAddress` | pass `Client: nil` in `openOTLPSink` |

The tests of the openers live in `cmd/opendustpan/auditsinks_test.go` beside
`openTestSinks`; the URL test lives in `pkg/logsink/webhook`. Authorization at
the point of use does not apply: no principal names an object here.

## ADR check

**ADR 60 decisions 12 and 15 (not owner-tagged).** Decision 12: "`webhook`: an
HTTPS POST of a JSON array of records. ... `otlp`: OpenTelemetry logs over
OTLP/HTTP with the JSON encoding." Decision 15: "The operator sets each sink at
start, with the flags `--audit-sink` and `--log-sink` and their environment
variables." Changed by: ADR 63, 64, 65, 68, 72, 126, 151, 210, 240 and 334.
Current rule: this ADR with those. ADR 334 (owner, 2026-11-07) extends decision
12: the OTLP sink takes `?ca-file=`, and both sinks write a float that is not
finite as text. The fix adds the egress client and changes no URL option. No
conflict.

**ADR 145 decision 20 (not owner-tagged).** "The senders use
`egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)`. Its dial guard checks
the resolved address of each connection, so a DNS answer that changes after the
check cannot reach a private, loopback or link-local address." Changed by: ADR
146, 149, 197, 203, 204, 210, 234, 241, 248, 262, 315 and 334. Current rule:
this ADR with those. ADR 334 (owner) changes decisions 13 and 20: the strict
host list applies at save time and at send time. The fix applies the same
client to the audit and log sinks. No conflict.

**ADR 250 decisions 1, 4 and 8 (the owner decided the ADR; no decision carries
its own owner tag).** "Never only one layer." "Each layer must stop the attack
alone, without help from the others." "Every layer has its own test." The fix
lists each layer and its test. No conflict.

**ADR 321 decision 1 (PM).** "Each outbound connection of a sign-in method goes
through the egress rules (PM). ... `internal/oauth` moves to this client." Not
owner-tagged. Changed by: ADR 332; decision 1 stands. Current rule: this ADR
with ADR 332. The fix does not change the sign-in clients. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none

Decided by the PM on 2026-11-09 (owner mandate for the week; rule 16):
1. A receiver on a private network: the change adds the static setting for
   the allowed private ranges of the sinks (IN above), empty by default. It
   reuses `egress.Rules` and `egress.ParseAllowedRanges`, the same mechanism
   as the LDAP and Git host settings, so no new egress logic. Benchmark: deny
   by default with an explicit operator allowlist; the escape hatch keeps a
   private SIEM or collector working. The setting never opens loopback,
   link-local, cloud metadata or multicast addresses (`egress.Rules`).
