## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test TestClaimerClaimWritesAuditEntry in internal/api/runnerapi/protocol_review_test.go calling ANY /runner/v1 with a runner token through the claimer gate. Check: TestClaimerClaimWritesAuditEntry fails on main, calling ANY /runner/v1 with a runner token through the claimer gate.
- [ ] 1.2 Add failing server tests for release, phase failed, and finish entries in the NEW file internal/api/runnerapi/claimer_audit_test.go calling ANY /runner/v1 with a runner token through the claimer gate. Check: TestClaimerReleaseWritesAuditEntry, TestClaimerPhaseFailedWritesAuditEntry and TestClaimerFinishWritesAuditEntry fail on main, each calling ANY /runner/v1 with a runner token.
- [ ] 1.3 Add failing server test for server-driven entries with reason lease-expiry, restart-retry, or server-release calling ANY /runner/v1 with a runner token through the claimer gate. Check: TestClaimerServerDrivenWritesAuditEntry fails on main in internal/api/runnerapi/claimer_audit_test.go, calling ANY /runner/v1 with a runner token through the claimer gate for the server-driven path.

## 2. Work

- [ ] 2.1 Write entries in the claimer service at commit time using the event mapping of pkg/job/state.go. Check: All five tests pass with the runner ID or server actor as subject.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Keep the existing read route ANY /api/audit/entries and show the claimer entries through it. Check: Route test shows ANY /runner/v1 with its claimer gate and ANY /api/audit/entries with its read actions.
- [ ] 4.2 Add an e2e step that claims and finishes work through ANY /runner/v1 with a runner token, then lists entries at ANY /api/audit/entries with an access token. Confirmed by: CI (make e2e). Check: E2E step lists the claimer entries.
- [ ] 4.3 Write the docs page note for claimer audit entries. Check: Docs page names the covered events, the subject, and the read route ANY /api/audit/entries.
- [ ] 4.4 Run the usability check of the read path: the site administrator lists entries from the docs page alone. A person's step. Check: Each denial names the one grant command.
- [ ] 4.5 Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings.
