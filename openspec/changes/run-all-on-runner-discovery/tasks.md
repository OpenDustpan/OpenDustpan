## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for discovery job start on wrapper claim through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: TestDiscoveryStartsOneJob fails on main and passes after the change; it exercises ANY /runner/v1 with a runner token in the pool namespace.
- [ ] 1.2 Add failing server test for preview output with 3 unit references through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: TestDiscoveryPreviewListsUnits fails on main and passes after the change; it reads the preview through ANY /runner/v1 with a runner token.
- [ ] 1.3 Add failing hostile tests for path escape with symlinks, unit count limit, foreign token, and 300 second timeout through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: TestDiscoveryRefusesEscape, TestDiscoveryRefusesLimit, TestDiscoveryRefusesForeignToken, and TestDiscoveryTimeout fail on main and pass after the change.
- [ ] 1.4 Add failing audit tests for start, complete, failed, and refused entries through ANY /runner/v1 with a runner token and read through ANY /api/audit/entries with an access token passing the audit read gate. Check: TestDiscoveryAuditNamesCause and TestDiscoveryAuditRefusal fail on main and pass after the change.

## 2. Build

- [ ] 2.1 Wire discovery operation through ANY /runner/v1 with a runner token passing the runner authorization gate with namespace-scoped authorization. Check: Claim through ANY /runner/v1 with a runner token starts a terragrunt-discover job in the pool namespace.
- [ ] 2.2 Write audit entries for discovery start, complete, failed, and refused through ANY /api/audit/entries with an access token passing the audit read gate. Check: Read ANY /api/audit/entries with an access token and check the start and complete entries.
- [ ] 2.3 Add repeat check on the idempotency key for the discovery record through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: Run discovery twice with the same Idempotency-Key header and check one discovery record.

## 3. Definition of done

- [ ] 3.1 Add e2e step against the real runner and the real directory over 3 units through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: Real runner claims a wrapper job over 3 Terragrunt units through ANY /runner/v1 with a runner token and the preview lists 3 unit references with no state version.
- [ ] 3.2 Write the Terragrunt discovery docs page in the same pull request. Check: Docs page command sequence runs as written against a test installation.
- [ ] 3.3 Run the usability check of the discovery path. Check: Runner operator completes discovery from the docs page alone and each error names the one command that helps.
- [ ] 3.4 Run the full check suite. Check: `make check` with zero findings.
