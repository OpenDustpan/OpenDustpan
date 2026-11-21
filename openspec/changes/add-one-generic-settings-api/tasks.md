## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Write failing tests for GET on both scopes and a namespace refusal in the NEW file internal/api/server/settings_get_test.go. Check: TestSettingsGetEffectiveSourceLockedRevision
- [x] 1.2 Write failing tests for PATCH refusals, atomic apply, stale revision, masked audit and audit failure in the NEW file internal/api/server/settings_patch_test.go; TestSettingsPatchRefusals exists on main in internal/api/server/settings_test.go, so extend it there. Check: TestSettingsPatchRefusals and TestSettingsPatchAtomic
- [x] 1.3 Write failing tests for the fresh step, the token refusal and idempotent replay in the NEW file internal/api/server/settings_fresh_test.go. Check: TestSettingsPatchFreshStep and TestSettingsPatchIdempotency
- [x] 1.4 Write failing tests for the session limit, the create audit entry and the host reset in the NEW file pkg/tempadmin/session_limit_test.go. Add the field SessionLimit to HostInput in pkg/accesstoken/host.go, so that the record end is the shorter of the setting and the token policy maximum, and pass the stored limit in cmd/opendustpan/host.go. Check: TestTempAdminSessionLimit, TestTempAdminCreateAuditNamesLimit and TestHostResetSessionLimit
- [x] 1.5 Add the NEW catalog code settings.revision-stale to internal/errcode/catalog_settings.go, and make the settings routes answer a stale revision with 412 and this code in place of request.precondition-failed (internal/api/server/settings.go, the If-Match check of the patch). Add the line for the new code to internal/errcode/testdata/catalog/settings.txt, and the 412 row to the error table in docs/reference/settings-api.md. Write the NEW test TestPatchStaleRevisionAnswersRevisionStale in the NEW file internal/api/server/settings_stale_test.go. Check: the NEW test TestPatchStaleRevisionAnswersRevisionStale fails on main.
- [x] 1.6 In internal/api/server/settings.go, keep the statuses of main for a body that is not valid: 400 with settings.key-unknown for an undeclared key and 400 with settings.value-invalid for a value outside its range, as the tool-neutral API decides (a key that is not valid answers 400). Write the NEW test TestPatchRefusesUnknownKeyWith400 in the NEW file internal/api/server/settings_unknown_test.go. Check: the NEW test TestPatchRefusesUnknownKeyWith400 passes on main.

- [x] 1.7 SECURITY. Build ParseTempAdminLimit in the NEW file pkg/tempadmin/session_limit.go. It checks the range of 1 millisecond to 24 hours and refuses a value outside it. Check: TestParseTempAdminLimitRange passes.
- [x] 1.8 SECURITY. Build the host reset of the stored session limit in the NEW file pkg/tempadmin/session_limit_reset.go. It needs a reason, refuses in recovery mode 3 and writes an audit entry with the reason. Check: TestHostResetSessionLimit passes.
- [x] 1.9 SECURITY. Build the idempotency-key replay in internal/api/server/settings.go. Update in pkg/store/sqlite/settings.go and pkg/store/postgres/settings.go calls finishCompletion(ctx, q, res) as the last step of its transaction, so that the middleware stores the answer. A replay with the same body answers the stored result. The same key with another body is refused. Check: TestSettingsPatchIdempotency passes.
- [x] 1.10 SECURITY. Write a hostile test that sends a value of 48 hours, a non-numeric value and a negative value straight to ParseTempAdminLimit, in the NEW file pkg/tempadmin/session_limit_parse_test.go. Check: TestParseTempAdminLimitHostile refuses each value.
- [x] 1.11 SECURITY. Write a hostile test that calls the host reset with no reason and in recovery mode 3, in the NEW file pkg/tempadmin/session_limit_reset_test.go. Check: TestHostResetSessionLimitHostile refuses both calls and the stored limit stays.
- [x] 1.12 SECURITY. Write a hostile test that sends the same Idempotency-Key with another body straight to the settings handler, in the NEW file internal/api/server/settings_replay_test.go. Check: TestSettingsPatchKeyReuseHostile refuses the request and changes nothing.

## 2. Side effects (computed from main)

- [x] 2.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 3. Definition of done

- [x] 3.1 Wire both routes with actions server:manage-settings (the name that main uses in internal/api/server/settings.go) and namespace:manage-settings, and list tempadmin.SessionLimitSettingDeclarations() in SettingDeclarations() in internal/cli/settings_api.go. Check: The route policy test lists both routes with their actions.
- [x] 3.2 Add the proposed amendment text of ADR 245 decision 10 for the owner in the pull request description (docs/adr takes only numbered ADR files, so no file is added there). Check: The pull request lists the text and the owner confirmation.
- [x] 3.3 Add an e2e step in the existing file e2e/settings_test.go (extend it in place) on `PATCH /api/dustpan/v1/settings` with a console session cookie that holds a fresh step, and the same weaker patch with an access token that is refused. BLOCKED by the missing e2e helper that makes a console session cookie with a fresh step; the step adds it. Then run the real Pulumi CLI and OpenTofu with a token. Check: make e2e step settings passes.
- [x] 3.4 Write the docs page in the NEW file docs/guides/settings.md. Check: make docs-lint passes
- [x] 3.5 Run the usability check from the docs page alone. Check: Each error names the one command that helps.
- [ ] 3.6 Run the full gate. Check: make check with zero findings.
