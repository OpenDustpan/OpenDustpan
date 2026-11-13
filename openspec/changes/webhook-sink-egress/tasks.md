## 1. The sink client

- [x] 1.1 `cmd/opendustpan/auditsinks.go`: add the egress rules and the client options to `sinkConfig`; `openWebhookSink` and `openOTLPSink` build the client with `egresshttp.ClientWith(cfg.rules, egress.NoRedirect, egress.HTTPSOnly, cfg.options)` and pass it as `Client`. Check: `TestWebhookSinkRefusesBlockedAddress` fails when the opener passes `Client: nil`.
- [x] 1.2 The two openers run `egress.CheckURL(rawURL, egress.HTTPSOnly)` and wrap a refusal with the sink item, never with the URL. Check: `TestAuditSinksRefuseBlockedURLAtStart` fails when the call goes.
- [x] 1.3 The comment on the start check says that the check is a defense-in-depth layer and names the layers that it does not rely on (the URL check of the client and the dial guard). Check: `golangci-lint run ./cmd/opendustpan/...` reports nothing, and the review of the diff sees the comment.
- [x] 1.4 `openAuditSinks` and `openSinkHandler` (`cmd/opendustpan/logsinks.go`) pass the rules and the options to `openSink`. Check: `go build ./...` and `rg 'cfg.rules' cmd/opendustpan` find each caller.

## 2. The tests, one for each layer

- [x] 2.1 `cmd/opendustpan/auditsinks_test.go`: `TestAuditSinksRefuseBlockedURLAtStart` sets a `webhook:` item and an `otlp:` item with a metadata or loopback IP literal and expects the sink error and the item name. Check: remove the `egress.CheckURL` call from `openWebhookSink`; the test fails.
- [x] 2.2 `TestWebhookSinkRefusesBlockedAddress` and `TestOTLPSinkRefusesBlockedAddress` set a host name that a test resolver answers with a loopback address, trust the certificate of the test server, and expect `egresshttp.ErrDialRefused` and no request at the receiver. Check: pass `Client: nil`; each test fails.
- [x] 2.3 `pkg/logsink/webhook/webhook_test.go`: `TestWebhookSinkRefusesBlockedURL` gives the adapter a URL with a loopback literal and the egress client and expects `egress.ErrRefused` with no `egresshttp.ErrDialRefused`. Check: remove the `checkURL` transport of `egresshttp.ClientWith`; the test fails.
- [x] 2.4 `TestWebhookSinkRefusedAddressGetsNothing` counts the requests of the receiver and reads the sink status after the refused round. Check: count the refused round as delivered; the test fails.
- [x] 2.5 `TestWebhookSinkStopsASlowReceiver` gives a receiver that does not answer and expects the attempt to end at `egress.Timeout`. Check: remove `Timeout` from `egresshttp.ClientWith`; the test fails.

## 3. The e2e step and the docs

- [x] 3.1 `e2e/sinks.go`: the delivery steps start the in-process server with `dustpan.WithAuditSinks` and a sink client whose rules allow loopback for the test, as `chat.NewLoopback` does for the chat sender. Check: `go test -tags e2e ./e2e/ -run TestAuditSinks`.
- [x] 3.2 The binary step proves the refusal: an `--audit-sink webhook:` item with a loopback address fails the start and names the item. Check: the same test with `E2E_SERVER=binary`.
- [x] 3.3 `docs/guides/audit-log.md`: the Webhook and OTLP sections state the egress rules, the refusal at the start and at each connection, and the sink status that shows it. Check: `make docs-lint` exits 0.
- [x] 3.4 `go test ./cmd/opendustpan/... ./pkg/logsink/...` and `go vet ./cmd/opendustpan/...` report nothing. Done: CI at the head of the pull request runs these checks and is green.
- [x] 3.5 `make check` reports zero findings. Done: CI at the head of the pull request is green for go, lint, golangci-lint, nilaway, scan, e2e and the fault and upgrade suites. CI skips the store suites, because the change touches no store code.
