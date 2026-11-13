## 1. Failing tests first

- [ ] 1.1 Add failing tests for last-used time, expiry 401, revoked 401, maximum 422, inactivity disable and warning field plus email. Check: Failing API test for last-used update on token use.
- [ ] 1.2 Add failing e2e steps for pulumi up and tofu apply with expired and idle tokens. Check: Failing e2e test for pulumi up with expired token gives 401.

## 2. Build

- [ ] 2.1 Store last-used time, expiry, maximum check and inactivity disable with audit entries and break-glass exemption. Check: `go test ./pkg/accesstoken/... -run TestLifeCycle -count=1` passes.
- [ ] 2.2 Wire warning field, console banner data, SMTP email queue with retry key and CLI warning. Check: `go test ./internal/notify/... -run TestExpiryWarning -count=1` passes.
- [ ] 2.3 Run e2e step 6 with test SMTP server for both CLIs and check audit entries. Check: `make e2e` step 6 passes against the real binary over TLS on SQLite and PostgreSQL.

## 3. Definition of done

- [ ] 3.1 Confirm wiring, routes with actions, token lifetime docs page content, usability run and make check with zero findings. Check: Route test lists token routes with authorization actions and public list unchanged.
