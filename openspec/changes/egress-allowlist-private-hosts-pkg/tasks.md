## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for allowlisted pass, non-listed refuse, partial multi-answer and always-refused ranges in pkg/egress/rules_test.go Check: TestAllowedRangeOpensPrivateAddressesOnly in pkg/egress/rules_test.go:192 sits next to the new cases and they fail on main
- [ ] 1.2 Add failing tests for redirect ErrRefused and legacy host read-path in pkg/notify/notify_test.go Check: TestStoredHostThatTheEgressRulesRefuse in pkg/notify/notify_test.go:1115 sits next to the new cases and they fail on main

## 2. Change

- [ ] 2.1 Add allowEntry with constructor and per-connection check in pkg/egress/egress.go Check: `go test ./pkg/egress/...` passes
- [ ] 2.2 Wire recheck on each dial and redirect with pinned-IP dial and proxy ignore in internal/egress/egresshttp/egresshttp.go Check: `go test ./internal/egress/egresshttp/...` passes with hostile DNS to 10.0.0.5 and 169.254.169.254
- [ ] 2.3 Add operator flag and env parsing and the config-to-Rules wiring with startup refusal and host-only startup log in the NEW file cmd/opendustpan/egressallow.go, and the Helm value in deploy/helm/opendustpan/values.yaml Check: `go test ./cmd/opendustpan/...` passes for bad CIDR, missing CIDRs and 0.0.0.0/0 refusal and the host-only startup log
- [ ] 2.4 Wire sink create and test to the rules with host-only audit in internal/api/notifications/handlers.go Check: POST /api/dustpan/v1/namespaces/{}/sinks/{}/test with an access token against a local receiver refuses without entry and passes with entry

## 3. Definition of done

- [ ] 3.1 Add operator docs page for flag, env and Helm settings, the startup log and the redirect note Check: The docs page runs as written and states that a chat redirect stays egress.ErrRefused
- [ ] 3.2 Run the e2e sink test step on POST /api/dustpan/v1/namespaces/{}/sinks/{}/test with an access token against a local receiver Check: The step passes on a private host with an entry
- [ ] 3.3 Run full checks and archive Check: `make check` with zero findings
