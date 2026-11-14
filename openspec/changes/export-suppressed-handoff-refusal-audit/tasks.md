## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing wiring test in pkg/dustpan/runners_test.go that the production runner API config sets OnHandoffRefusalSuppressed. Check: TestSuppressedHandoffRefusalWiringIsNotNil in pkg/dustpan/runners_test.go fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Done

- [ ] 3.1 Wire the callback in the runner area file of pkg/dustpan to the NEW Prometheus counter runner_handoff_audit_suppressed_total with zero labels and already-registered reuse. Check: TestSuppressedHandoffRefusalCounterIncrements in pkg/dustpan/runners_test.go passes with exactly-once increment, including the carried-count and written-entry cases.
- [ ] 3.2 Add route scrape and second-server reuse tests in pkg/dustpan/runners_test.go. They call the existing route ANY /api/server/metrics, served by internal/api/server (the HTTP adapter of the installation API), with an operator access token, and check the NEW counter name and help text. Check: TestSuppressedHandoffRefusalCounterVisibleAtMetrics shows the NEW counter at zero then after suppression, and TestSecondServerReusesCounterRegistration passes.
- [ ] 3.3 Document the NEW counter in the NEW file docs/reference/metrics.md and run the usability walk from the docs page alone. Confirmed by: CI (make check). Check: docs/reference/metrics.md lists runner_handoff_audit_suppressed_total with zero labels, reset-on-restart and rate() note.
