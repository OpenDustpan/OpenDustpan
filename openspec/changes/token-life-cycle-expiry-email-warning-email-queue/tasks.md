## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing queue tests for due warning, late-created token, stale token skip and missing address in `pkg/accesstoken/accesstoken_test.go`. Check: `go test ./pkg/accesstoken -run TestQueueWarningAtWarningTime -count=1` is NEW and fails on main.
- [ ] 1.2 Add failing email content test for owner address, token name, expiry date and no secret in NEW file `pkg/notify/warning_email_test.go`. Check: `go test ./pkg/notify -run TestWarningEmailContent -count=1` is NEW and fails on main.
- [ ] 1.3 Add failing TLS, opt-in, certificate and input tests for the SMTP sender in NEW file `internal/smtpsvc/smtp_test.go`. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` is NEW and fails on main.

## 2. Build

- [ ] 2.1 Build durable warning queue with states queued, sending, retry, sent and failed and one retry in NEW file `pkg/accesstoken/warning_queue.go`. Check: `go test ./pkg/accesstoken -run TestWarningQueue -count=1` is NEW and passes.
- [ ] 2.2 Wire SMTP sender to resolved installation settings with TLS rule, fixed poll, lease and backoff in NEW file `internal/smtpsvc/smtp.go`. Check: `go test ./internal/smtpsvc -run TestPlainRefused -count=1` is NEW and passes.
- [ ] 2.3 Write audit entry for each delivered, skipped or failed warning in NEW file `pkg/accesstoken/warning_audit.go`. Check: `go test ./pkg/accesstoken -run TestWarningAudit -count=1` is NEW and passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add e2e step in NEW file `e2e/token_life_cycle_expiry_email_warning_email_queue_test.go` with the test SMTP server and a personal access token used by the real CLI, with `PATCH /api/dustpan/v1/settings` to set the relay and sender; the audit check is BLOCKED by the audit entry kind that this change adds. Check: E2E against the real binary over TLS shows two warning messages on the test SMTP server. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write NEW docs page `docs/guides/expiry-warning-email.md` for SMTP settings, warning times, queue status and missing status. Check: Docs page commands run as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/token_life_cycle_expiry_email_warning_email_queue_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.3 Run usability check of the operator path from the docs page. Check: Operator completes setup from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.4 Run full check with zero findings. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
