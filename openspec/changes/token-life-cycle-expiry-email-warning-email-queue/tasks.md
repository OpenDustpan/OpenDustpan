## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing queue tests for due warning, late-created token, stale token skip and missing address. Check: `go test ./pkg/accesstoken -run TestQueueWarningAtWarningTime -count=1` is NEW and fails on main.
- [ ] 1.2 Add failing email content test for owner address, token name, expiry date and no secret. Check: `go test ./pkg/notify -run TestWarningEmailContent -count=1` is NEW and fails on main.
- [ ] 1.3 Add failing TLS, opt-in, certificate and input-validation tests for the SMTP sender. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` is NEW and fails on main.

## 2. Build

- [ ] 2.1 Build durable warning queue with states queued, sending, retry, sent and failed and one retry. Check: `go test ./pkg/accesstoken -run TestWarningQueue -count=1` passes.
- [ ] 2.2 Wire SMTP sender to resolved installation settings with TLS rule, fixed poll, lease and backoff. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` passes.
- [ ] 2.3 Write audit entry for each delivered, skipped or failed warning. Check: `go test ./pkg/accesstoken -run TestWarningAudit -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Add e2e step with the test SMTP server, a personal access token and `GET /api/dustpan/v1/namespaces/{namespace}/audit` that checks receipt and audit entries. Check: E2E against the real binary over TLS shows two warning messages on the test SMTP server and one audit entry per warning.
- [ ] 3.2 Write docs page for SMTP settings, warning times, queue status and missing status. Check: Docs page commands run as written in a test.
- [ ] 3.3 Run usability check of the operator path from the docs page. Check: Operator completes setup from the docs page alone and each error names the one command that helps.
- [ ] 3.4 Run full check with zero findings. Check: `make check` reports zero findings.
