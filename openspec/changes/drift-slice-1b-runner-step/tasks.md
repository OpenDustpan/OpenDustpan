## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing reduction test in NEW pkg/drift/summary_test.go. Check: TestReducePlanToDriftSummary fails on main. No route: the test reads a recorded plan file only.
- [ ] 1.2 Write the failing hostile summary tests in NEW pkg/drift/summary_test.go. Check: TestHostileSummaryChangesNothing fails on main. No route: the test calls Validate only.
- [ ] 1.3 Write the failing reconcile test in NEW internal/driftreconcile/reconcile_test.go. Check: TestReconcileRecordsAtPhaseEnd fails on main. No route: the test drives the reconcile with a fake job token only.
- [ ] 1.4 Write the spike test for client-side encryption in NEW pkg/drift/encrypted_spike_test.go. Check: TestEncryptedUnitGivesAddresses fails on main. No route: the test checks addresses only.
- [ ] 1.5 Write the failing idempotence test for double reconcile in NEW internal/driftreconcile/reconcile_test.go. Check: TestDoubleReconcileRecordsOnce fails on main. No route: the test replays Record with the same job ID only.

## 2. Build

- [ ] 2.1 Write Validate in NEW pkg/drift/summary.go and the reconcile in NEW internal/driftreconcile/reconcile.go, wired in pkg/dustpan/drift.go:21 (func WithDriftStore). Check: The drift read route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift with an access token reads drifted after a valid summary; runner transport is BLOCKED by #1374.
- [ ] 2.2 Edit the existing drift docs page at NEW docs/guides/drift.md and its reference page to remove the sentence that says no runner records a result. Check: The existing drift docs page holds no sentence that says no runner records a result.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the reconcile to the drift routes with their authorization actions. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift with an access token, gated by unit:run-plan at start.
- [ ] 4.2 Run the drift suites on both stores without the real OpenTofu CLI. Check: BLOCKED by slice 1c; this slice proves reduction and reconcile on both stores without the real CLI.
- [ ] 4.3 Update the docs page in the same pull request. Check: The drift docs page update ships in the same pull request.
- [ ] 4.4 Walk the usability path from the docs page alone. A person's step. Check: The persona completes the path from the docs page alone and each error names the drift read command.
- [ ] 4.5 Run `make check` with zero findings. Confirmed by: CI (make check). Check: `make check` reports zero findings.
