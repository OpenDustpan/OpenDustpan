## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. tests first

- [ ] 1.1 Write failing tests for each spec scenario in NEW file pkg/backup/restore_access_setting_test.go (package backup). TestRestoreAccessSettingDefaultOff is NEW. Check: TestRestoreAccessSettingDefaultOff fails on main

## 2. build

- [ ] 2.1 Add the stored boolean key with fail-closed parse in NEW file pkg/backup/restore_access_setting.go (the NEW restoreAccessSetting constructor), and register the key in internal/cli/settings.go. Check: `go test ./pkg/backup/... -run TestRestoreAccessSetting -count=1`
- [ ] 2.2 Branch the restore apply step on the snapshot with the two-path guard in pkg/backup/service.go. Check: `go test ./pkg/backup/... -run TestRestoreAccess -count=1`

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. definition of done

- [ ] 4.1 Route test lists the settings route with its actions. Check: Route test passes
- [ ] 4.2 E2E: PATCH /api/dustpan/v1/settings with a console session cookie (fresh step, never an access token), then POST /api/server/restores with the same session, over TLS. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Add the docs page NEW file docs/guides/restore-access-setting.md, and list the value in the admin access report in the existing file pkg/backup/service.go. Check: Docs page and report show the value
- [ ] 4.4 Usability walk from the docs page alone. Check: Each error names one command. A person's step.
- [ ] 4.5 Run full checks. Check: `make check` has zero findings. Confirmed by: CI (the agent runs the fast checks only).
