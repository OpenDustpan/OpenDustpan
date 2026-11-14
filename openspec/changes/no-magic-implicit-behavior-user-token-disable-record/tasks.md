## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 In NEW `internal/token/autodisable_audit_test.go` add failing test TestAutomaticTokenDisableWritesAuditEntry that forces an expiry disable and calls `ANY /api/audit/entries` with an access token past the audit read gate. Check: NEW internal/token/autodisable_audit_test.go TestAutomaticTokenDisableWritesAuditEntry fails on main
- [ ] 1.2 In NEW `internal/token/autodisable_audit_test.go` add failing test TestAutomaticTokenDisableAuditHasNoSecret that calls `ANY /api/audit/entries` with an access token past the audit read gate and shows the entry carries the stable token ID and no secret value. Check: NEW internal/token/autodisable_audit_test.go TestAutomaticTokenDisableAuditHasNoSecret fails on main

## 2. Definition of done

- [ ] 2.1 Wire the audit write into the automatic expiry-disable path in `internal/token`, document the entry on the docs page, and run the usability check of `ANY /api/audit/entries` with an access token past the audit read gate and `make check` with zero findings. Confirmed by: CI (`make check`) Check: `GET /api/audit/entries` with an access token past the audit read gate returns the disable entry
