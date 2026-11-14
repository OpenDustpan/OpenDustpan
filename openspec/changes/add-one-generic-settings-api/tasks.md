## 1. Failing tests first

- [ ] 1.1 Write failing tests for GET on both scopes, and a namespace-authorization refusal test. Check: TestSettingsGetEffectiveSourceLockedRevision and TestNamespaceSettingsRefuseWithoutAction
- [ ] 1.2 Write failing tests for PATCH atomic, null, unknown key, file-only key, locked key, invalid value, stale revision and audit failure. Check: TestSettingsPatchRefusals, TestSettingsPatchAtomic and TestSettingsPatchAuditFailure
- [ ] 1.3 Write failing tests for the fresh step, including a mixed patch, and for idempotency replay, missing key and replay during the first request. Check: TestSettingsPatchFreshStep, TestSettingsPatchMixedDirections and TestSettingsPatchIdempotency
- [ ] 1.4 Write failing tests for the session limit: default cap, token policy cap, new records only, bad stored value, host reset option, valid fresh step with invalid value, stale revision on the limit key, reload audit with source change, and a creation during a PATCH. Check: TestTempAdminSessionLimit, TestHostResetSessionLimit and TestSettingsReloadMovesRevision

## 2. Definition of done

- [ ] 2.1 Wire the routes with actions installation:manage-settings and namespace:manage-settings, and audit entries. Check: The route policy test lists both routes with their actions and none is public.
- [ ] 2.2 Add an e2e step for a settings read and write against the real server, and CLIs that still work. Check: make e2e step settings passes
- [ ] 2.3 Check that the API description serves what the console settings page needs: locked flag, source, revision and the refusal text. Record the console page work in the console repository. Check: The API description test lists each field, and the console issue links this change.
- [ ] 2.4 Write the docs page for the settings resource and the session limit. Check: make docs-lint passes
- [ ] 2.5 Run the usability check from the docs page alone. Check: Each error names the one command that helps.
- [ ] 2.6 Run the full gate. Check: make check with zero findings
