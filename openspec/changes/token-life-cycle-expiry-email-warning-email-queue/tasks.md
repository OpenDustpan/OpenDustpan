## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing queue tests for due warning, late-created token, stale token skip and missing address. Check: `go test ./pkg/accesstoken -run TestQueueWarningAtWarningTime -count=1` is NEW and fails on main.
- [ ] 1.2 Add failing email content test for owner address, token name, expiry date and no secret. Check: `go test ./pkg/notify -run TestWarningEmailContent -count=1` is NEW and fails on main.
- [ ] 1.3 Add failing TLS, opt-in, certificate and input tests for the SMTP sender. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` is NEW and fails on main.

## 2. Build

- [ ] 2.1 Build durable warning queue with states queued, sending, retry, sent and failed and one retry. Check: `go test ./pkg/accesstoken -run TestWarningQueue -count=1` is NEW and passes.
- [ ] 2.2 Wire SMTP sender to resolved installation settings with TLS rule, fixed poll, lease and backoff. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` is NEW and passes.
- [ ] 2.3 Write audit entry for each delivered, skipped or failed warning. Check: `go test ./pkg/accesstoken -run TestWarningAudit -count=1` is NEW and passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step with the test SMTP server and a personal access token used by the real CLI; the audit check is BLOCKED by the audit entry kind that this change adds. Check: E2E against the real binary over TLS shows two warning messages on the test SMTP server.
- [ ] 4.2 Write docs page for SMTP settings, warning times, queue status and missing status. Check: Docs page commands run as written in a test.
- [ ] 4.3 Run usability check of the operator path from the docs page. Check: Operator completes setup from the docs page alone and each error names the one command that helps.
- [ ] 4.4 Run full check with zero findings. Check: `make check` reports zero findings.
