---
version: 1
---

Issue: #1174, #1341, #1372, #2222.

## Problem

Operator wants to post notifications to a private chat or webhook host without opening the whole private network. Today every private dial is refused or needs a wide exception, so the operator cannot scope one host with its CIDRs from flag, env or Helm.

## Evidence

- pkg/egress and internal/egress/egresshttp exist on main Proof: paths internal/egress/egresshttp and pkg/egress with exists true
- egresshttp refuses a refused address with egress.ErrRefused Proof: internal/egress/egresshttp/egresshttp.go:22 and :28 name egress.ErrRefused for a refused connection
- Main has a test that a private address opens only in an allowed range Proof: pkg/egress/rules_test.go:192 (TestAllowedRangeOpensPrivateAddressesOnly)

## Fix boundary

- Operator-only allowlist of exact host plus required CIDRs in pkg/egress, empty by default, logged at startup without URLs
- Per-connection and post-redirect resolved-address check in egresshttp with single-IP dial; credentials stay on the first host; refused sink create or test audits the host only
- RFC 98 text aligned so a chat redirect stays egress.ErrRefused; legacy chat host uses read-path validation at send time

Out of scope:

- New chat renderers for Mattermost, Matrix and Teams
- Sealing of webhook URLs at rest
- Refused code-location audit entry at drift and job start
- Runner and launcher fetch enforcement beyond docs; that issue stays open and this change closes the sink paths only

## Check

- Add failing tests for allowlisted pass, non-listed refuse, partial multi-answer and always-refused ranges in pkg/egress/rules_test.go Check: TestAllowedRangeOpensPrivateAddressesOnly in pkg/egress/rules_test.go:192 sits next to the new cases and they fail on main
- Add failing tests for redirect ErrRefused and legacy host read-path in pkg/notify/notify_test.go Check: TestStoredHostThatTheEgressRulesRefuse in pkg/notify/notify_test.go:1115 sits next to the new cases and they fail on main
- Add allowEntry with constructor and per-connection check in pkg/egress/egress.go Check: `go test ./pkg/egress/...` passes
- Wire recheck on each dial and redirect with pinned-IP dial and proxy ignore in internal/egress/egresshttp/egresshttp.go Check: `go test ./internal/egress/egresshttp/...` passes with hostile DNS to 10.0.0.5 and 169.254.169.254
- Add operator flag and env parsing and the config-to-Rules wiring with startup refusal and host-only startup log in the NEW file cmd/opendustpan/egressallow.go, and the Helm value in deploy/helm/opendustpan/values.yaml Check: `go test ./cmd/opendustpan/...` passes for bad CIDR, missing CIDRs and 0.0.0.0/0 refusal and the host-only startup log
- Wire sink create and test to the rules with host-only audit in internal/api/notifications/handlers.go Check: POST /api/dustpan/v1/namespaces/{}/sinks/{}/test with an access token against a local receiver refuses without entry and passes with entry
- Add operator docs page for flag, env and Helm settings, the startup log and the redirect note Check: The docs page runs as written and states that a chat redirect stays egress.ErrRefused
- Run the e2e sink test step on POST /api/dustpan/v1/namespaces/{}/sinks/{}/test with an access token against a local receiver Check: The step passes on a private host with an entry

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines egress.ErrRefused for a refused address (`pkg/egress/egress.go:39 (const ErrRefused)`)
- Main checks chat sink targets in Sender.checkTarget (`pkg/notify/chat/sender.go:221 (func Sender.checkTarget)`)
- Main returns the host of a chat webhook in lower case (`pkg/notify/route.go:328 (func ChatHost)`)
- Main checks code location repositories in CheckRepository (`pkg/job/location.go:93 (func CheckRepository)`)
- Main builds the sink document in the notifications handlers (`internal/api/notifications/handlers.go:104 (func toDocument)`)
- Main has a stored-host egress refusal test for notify (`pkg/notify/notify_test.go:1115 (TestStoredHostThatTheEgressRulesRefuse)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any accepted ADR decide egress, audit or operator settings here? No. The pack ADR list is empty and the only RFC is 0098 with status discussing. The change follows the rules in the issue texts.
- Does a redirect from a chat webhook stay egress.ErrRefused or map to ErrPermanent? It stays egress.ErrRefused. The client uses egress.NoRedirect, and both classes stop delivery. The RFC text is aligned to the code.
- Why do host scenarios use two error classes? Form errors at config and sink create use sink.invalid. Dial refusals use egress.ErrRefused.
- How do TLS SNI and proxy behave with single-IP dial? TLS sends the host name in SNI and verifies it while TCP dials the pinned IP. The sink client ignores proxy environment variables.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
