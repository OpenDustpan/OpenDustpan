## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing server test for GET settings SMTP keys with value and source in NEW internal/settingsvc/settings_test.go Check: TestSmtpSettingsShowEffectiveValueAndSource in NEW internal/settingsvc/settings_test.go calls GET /api/dustpan/v1/settings with access token and fails on main
- [ ] 1.2 Add failing server test for PATCH SMTP password refused with 409 in NEW internal/settingsvc/settings_test.go Check: TestSmtpPasswordPatchConflict in NEW internal/settingsvc/settings_test.go calls PATCH /api/dustpan/v1/settings with access token and fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Implement hosted-service marker with fail-closed off and settings.locked refusal in NEW internal/settingsvc/smtp_settings.go Check: TestSmtpHostedPlainRefused calls PATCH /api/dustpan/v1/settings with access token and fails on main
- [ ] 3.2 Implement SMTP connection check that reports reachability and writes an audit entry in NEW internal/settingsvc/smtpcheck.go Check: TestSmtpConnectionCheckAudit calls NEW POST /api/dustpan/v1/settings/smtp-check with access token and fails on main
- [ ] 3.3 Add guide page and e2e step against a test server for SMTP settings read and check in NEW docs/guides/smtp-settings.md. Confirmed by: the driver's gates. Check: E2E smtp-settings-read calls GET /api/dustpan/v1/settings with access token against test server
- [ ] 3.4 Run usability check from docs page alone for SMTP settings path. A person's step. Check: Usability run calls GET /api/dustpan/v1/settings and PATCH /api/dustpan/v1/settings with access token from docs page alone
