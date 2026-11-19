## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. tests first

- [x] 1.1 Write failing tests for each spec scenario in NEW file pkg/backup/restore_access_setting_test.go (package backup). TestRestoreAccessSettingDefaultOff is NEW. Check: TestRestoreAccessSettingDefaultOff fails on main

## 2. build

- [x] 2.1 Add the stored boolean key with fail-closed parse in NEW file pkg/backup/restore_access_setting.go (the NEW restoreAccessSetting constructor), and register the key through SettingDeclarations() in the existing file internal/cli/settings_api.go (line 50), with the declaration in the same NEW file (Database: true, no flag or env layer), like the tempadmin session limit declaration. Check: `go test ./pkg/backup/... -run TestRestoreAccessSetting -count=1`
- [x] 2.2 Branch the restore apply step on the snapshot with the two-path guard in the existing file pkg/backup/restore_steps.go (the ApplyRestoreArea call at line 1093 and restoreAuditObject at line 1537), and read the flag where it comes from in the existing file internal/api/server/restores.go (line 199, KeepAccessTables). Check: `go test ./pkg/backup/... -run TestRestoreAccess -count=1`

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. definition of done

- [x] 4.1 Route test lists the settings route with its actions. Check: Route test passes
- [x] 4.2 E2E: PATCH /api/dustpan/v1/settings with a console session cookie (fresh step, never an access token), then POST /api/dustpan/v1/restores with the same session, over TLS. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [x] 4.3 Add the docs page NEW file docs/guides/restore-access-setting.md, and list the value in the restore audit object in the existing function restoreAuditObject of pkg/backup/restore_steps.go. Check: Docs page and report show the value. `make docs-lint` passes on each edited guide.
- [ ] 4.4 Usability walk from the docs page alone. Check: Each error names one command. A person's step.
- [ ] 4.5 Run full checks. Check: `make check` has zero findings. Confirmed by: CI (the agent runs the fast checks only).
