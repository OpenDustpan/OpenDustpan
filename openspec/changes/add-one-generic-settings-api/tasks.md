## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for GET on both scopes and a namespace refusal in the NEW file internal/api/server/settings_get_test.go. Check: TestSettingsGetEffectiveSourceLockedRevision
- [ ] 1.2 Write failing tests for PATCH refusals, atomic apply, stale revision, masked audit and audit failure in the NEW file internal/api/server/settings_patch_test.go. Check: TestSettingsPatchRefusals and TestSettingsPatchAtomic
- [ ] 1.3 Write failing tests for the fresh step, the token refusal and idempotent replay in the NEW file internal/api/server/settings_fresh_test.go. Check: TestSettingsPatchFreshStep and TestSettingsPatchIdempotency
- [ ] 1.4 Write failing tests for the session limit, the create audit entry and the host reset in the NEW file pkg/tempadmin/session_limit_test.go. Check: TestTempAdminSessionLimit, TestTempAdminCreateAuditNamesLimit and TestHostResetSessionLimit
- [ ] 1.5 Add the NEW catalog code settings.revision-stale to internal/errcode/catalog_settings.go, and make the settings routes answer a stale revision with 412 and this code in place of request.precondition-failed (internal/api/server/settings.go, the If-Match check of the patch). Write the NEW test TestPatchStaleRevisionAnswersRevisionStale in the NEW file internal/api/server/settings_stale_test.go. Check: the NEW test TestPatchStaleRevisionAnswersRevisionStale fails on main.
- [ ] 1.6 In internal/api/server/settings.go, keep the statuses of main for a body that is not valid: 400 with settings.key-unknown for an undeclared key and 400 with settings.value-invalid for a value outside its range, as the tool-neutral API decides (a key that is not valid answers 400). Write the NEW test TestPatchRefusesUnknownKeyWith400 in the NEW file internal/api/server/settings_unknown_test.go. Check: the NEW test TestPatchRefusesUnknownKeyWith400 passes on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire both routes with actions installation:manage-settings and namespace:manage-settings. Check: The route policy test lists both routes with their actions.
- [ ] 3.2 Add the proposed amendment text of ADR 245 decision 10 for the owner. Check: The pull request lists the text and the owner confirmation.
- [ ] 3.3 Add an e2e step on `PATCH /api/dustpan/v1/settings` with a console session cookie that holds a fresh step, and the same weaker patch with an access token that is refused. BLOCKED by the missing e2e helper that makes a console session cookie with a fresh step; the step adds it. Then run the real Pulumi CLI and OpenTofu with a token. Check: make e2e step settings passes
- [ ] 3.4 Write the docs page. Check: make docs-lint passes
- [ ] 3.5 Run the usability check from the docs page alone. Check: Each error names the one command that helps.
- [ ] 3.6 Run the full gate. Check: make check with zero findings
