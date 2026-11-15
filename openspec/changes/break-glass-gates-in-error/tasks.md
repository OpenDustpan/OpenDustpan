## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing tests of the scenarios in the NEW file `pkg/gate/breakglass_test.go`; TestBreakGlassChangeFailsClosed exists on main in `pkg/gate/statewrite_test.go`: extend it there, and when it passes on main from the start, the pull request body says so. Check: TestBreakGlassErrorPassesOnce, TestBreakGlassReplayRefuses, TestBreakGlassExpiryRefuses, TestBreakGlassBadReasonRefuses, TestBreakGlassDenyStillRefuses, TestBreakGlassWrongUnitOrKindRefuses and TestBreakGlassChangeFailsClosed fail on main.

## 2. Implementation

- [ ] 2.1 Add the pass type, the decision function and the fail-closed port in the NEW file `pkg/gate/breakglass.go`. Check: `go test ./pkg/gate/...` passes.
- [ ] 2.2 Add a conformance suite of the port for the follow-up adapters in the NEW file `pkg/gate/breakglass_conformance_test.go`. Check: The suite runs against an in-memory fake in `go test ./pkg/gate/...`.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 The route fields, the e2e step against the real CLI and the usable docs page for the pass: BLOCKED by break-glass-pass-wiring, which adds the pass store, the routes and the proof kind. Here, add a docs note that break-glass is not reachable yet. Check: `make docs-lint` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
