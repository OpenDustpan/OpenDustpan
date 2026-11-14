## 1. build

- [ ] 1.1 Add failing tests for default off refusal, off with server:restore-access success, allowed change, refused change without action, refused change without fresh step, and restore with setting on plus snapshot, invalid value, and race cases. Check: TestRestoreAccessSetting_defaultOff fails on main, passes after.
- [ ] 1.2 Add the setting with default off and the REST route with its actions and object. Check: `go test ./internal/api/server/... -run TestSettingsRoute -count=1` passes.
- [ ] 1.3 Branch the restore apply step on the snapshot value with fail-closed read and the two-path guard in the same transaction. Check: `go test ./internal/backup/... -run TestRestoreAccess -count=1` passes.

## 2. definition of done

- [ ] 2.1 Update the route test and the audit entry check for old and new value. Check: Route test lists the new settings route with its actions.
- [ ] 2.2 Add e2e coverage for the setting change and a restore. Check: e2e step changes the setting and runs a restore against the real binary over TLS.
- [ ] 2.3 Add the user docs page in the same pull request. Check: Docs page shows the setting, default, actions, fresh step, and audit record.
- [ ] 2.4 Run the usability walk from the docs page alone. Check: Persona completes the path from the docs page alone and each error names the one command that helps.
- [ ] 2.5 Run full checks. Check: `make check` passes with zero findings.
