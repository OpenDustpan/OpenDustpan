## 1. Failing tests

- [ ] 1.1 Add failing queue tests for due warning, stale token skip and missing address. Check: `go test ./pkg/accesstoken -run TestQueueWarningAtWarningTime -count=1` fails on main.
- [ ] 1.2 Add failing email content test for owner address, token name and expiry date. Check: `go test ./pkg/notify -run TestWarningEmailContent -count=1` fails on main.
- [ ] 1.3 Add failing TLS, opt-in and input-validation tests for the SMTP sender. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` fails on main.

## 2. Build

- [ ] 2.1 Build durable warning queue with states queued, sending, retry, sent and failed and one retry. Check: `go test ./pkg/accesstoken -run TestWarningQueue -count=1` passes.
- [ ] 2.2 Wire SMTP sender to installation settings with TLS rule, fixed poll, lease and backoff, and missing status. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` passes.
- [ ] 2.3 Write audit entry for each delivered, skipped or failed warning without a new token route. Check: `go test ./pkg/accesstoken -run TestWarningAudit -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Add e2e step against the real binary over TLS with the test SMTP server. Check: `make e2e TOKEN_EXPIRY=1` delivers two warning messages to the test SMTP server.
- [ ] 3.2 Write docs page for SMTP settings, warning times, queue status and missing status. Check: Docs page command for SMTP relay, sender, warning times and missing status runs as written.
- [ ] 3.3 Run usability check of the operator path from the docs page. Check: Operator completes setup from the docs page alone and each error names the one command that helps.
- [ ] 3.4 Run full check with zero findings. Check: `make check` reports zero findings.
