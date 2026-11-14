## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests first

- [ ] 1.1 Write the failing server test TestReleaseClaimStoresReason in the NEW file internal/api/runnerapi/release_reason_test.go. It calls the existing route ANY /runner/v1 with a runner token handoff credential and passes the claim handoff check. Check: TestReleaseClaimStoresReason fails on main.
- [ ] 1.2 Write the hostile test TestReleaseReasonHostileInputs in the NEW file internal/api/runnerapi/release_reason_hostile_test.go for 501 runes, CRLF, NUL, ESC, U+2028, and invalid UTF-8. Check: TestReleaseReasonHostileInputs fails on main.

## 2. Schema and store

- [ ] 2.1 Add the NEW reason column to the claim table in the baseline of each dialect in place, in pkg/store/sqlite and pkg/store/postgres, and extend ClaimRecord in pkg/runner/store.go. Check: Store conformance tests pass for both dialects.
- [ ] 2.2 Extend the Store port in pkg/runner with the release reason for third-party stores and add a conformance test in the NEW file pkg/runner/runnertest/release_reason.go covering adapters pkg/store/sqlite and pkg/store/postgres. Check: TestRunnerManagementReturnsReleaseReason passes.

## 3. Wiring

- [ ] 3.1 Pass the sanitized reason from releaseClaim in internal/api/runnerapi/protocol.go to the store and return it in the existing runner management claim read as the NEW field releaseReason. Check: TestReleaseClaimStoresReason passes against ANY /runner/v1 with the handoff token.

## 4. Side effects (computed from main)

- [ ] 4.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 4.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 5. Definition of done

- [ ] 5.1 Add the new file e2e/runner_release_reason_test.go (NEW file) for a runner release with a reason. It calls the existing route ANY /runner/v1 with a runner token handoff credential. Confirmed by: CI (e2e job). Check: The e2e step against ANY /runner/v1 with the runner handoff token shows the stored reason.
- [ ] 5.2 Add the new file docs/guides/runner-release-reason.md (NEW file) with the field, the 500-rune cap, and the curl command. Confirmed by: the driver's gates. Check: The docs page builds with make docs-lint.
- [ ] 5.3 Check that the Runner operator completes the read from the docs page alone and each error names one command. A person's step. Check: Usability check with the Runner operator from the docs page alone.
- [ ] 5.4 Run make check. Confirmed by: CI (make check). Check: Zero findings.
