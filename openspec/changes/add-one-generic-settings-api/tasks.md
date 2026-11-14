## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for GET on both scopes and a namespace refusal. Check: TestSettingsGetEffectiveSourceLockedRevision
- [ ] 1.2 Write failing tests for PATCH refusals, atomic apply, stale revision, masked audit and audit failure. Check: TestSettingsPatchRefusals and TestSettingsPatchAtomic
- [ ] 1.3 Write failing tests for the fresh step, the token refusal and idempotent replay. Check: TestSettingsPatchFreshStep and TestSettingsPatchIdempotency
- [ ] 1.4 Write failing tests for the session limit, the create audit entry and the host reset. Check: TestTempAdminSessionLimit, TestTempAdminCreateAuditNamesLimit and TestHostResetSessionLimit

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire both routes with actions installation:manage-settings and namespace:manage-settings. Check: The route policy test lists both routes with their actions.
- [ ] 3.2 Add the proposed amendment text of ADR 245 decision 10 for the owner. Check: The pull request lists the text and the owner confirmation.
- [ ] 3.3 Add an e2e step with a session that holds a fresh step and a token that is refused, then run the real Pulumi CLI and OpenTofu. Check: make e2e step settings passes
- [ ] 3.4 Write the docs page. Check: make docs-lint passes
- [ ] 3.5 Run the usability check from the docs page alone. Check: Each error names the one command that helps.
- [ ] 3.6 Run the full gate. Check: make check with zero findings
