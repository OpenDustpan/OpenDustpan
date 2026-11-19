## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the tests of the scenarios: single use, expiry, unit binding, grant, fresh proof, in the NEW file `pkg/gate/break_glass_pass_test.go`. Check: go test ./pkg/gate/... fails on main

- [ ] 1.2 SECURITY. Write the hostile test `TestBreakGlassPassBoundaryHostile` in the NEW file `pkg/gate/break_glass_pass_boundary_test.go`. Send a pass ID of wrong length and a random pass straight to the pass ID check. Each case must be refused, and each case must write an audit entry. Check: go test ./pkg/gate/... -run TestBreakGlassPassBoundaryHostile fails on main
- [ ] 1.3 SECURITY. Write the hostile test `TestBreakGlassPassParallelUse` in the NEW file `pkg/gate/break_glass_pass_store_test.go`. Run two parallel uses of one pass on each store dialect. Exactly one use must succeed. Check: go test ./pkg/gate/... -run TestBreakGlassPassParallelUse fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.3 SECURITY. Build the pass store in the NEW file `pkg/gate/break_glass_pass_store.go`. Use one conditional update on each dialect. The update marks the pass used only when the pass is unused, unexpired and for the unit. It runs in the same transaction as the audit entry. Check: `TestBreakGlassPassParallelUse` passes on each dialect.

- [ ] 3.1 BLOCKED by the owner gate until the lead confirms task 3.2. Wire the header X-Dustpan-Break-Glass-Pass on the routes that a gate in error refuses (the state write routes of break-glass-gates-in-error), with their authorization actions; add the e2e step, the docs page and the usability check. Check: make e2e and make check with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
- [ ] 3.2 Confirm that the real OpenTofu CLI, the real Pulumi CLI or the local wrapper can send the header X-Dustpan-Break-Glass-Pass on those routes, and record how in the pull request body. If none can send it, the question goes back to the owner. Check: the pull request body names the client that sends the header. A person's step.
- [ ] 3.4 SECURITY. Write the decision record before `Consume` uses the pass, so that a pass is never used with no record of the decision. Today `pkg/gate/statewrite.go` uses the pass first and writes the record after. Check: a test with a store whose `Records().Put` fails shows that no pass is used.
- [ ] 3.5 SECURITY. Tie `Record.BreakGlass` to the pass that the service used: the record names the pass ID that `Consume` returned, not the audit entry ID that the caller sends in `Change.BreakGlass`. Check: a test shows that a caller cannot name a pass or an audit entry that it did not use.
- [ ] 3.6 Move the conformance suite of the pass port from `pkg/gate/breakglass_conformance_test.go` into a `pkg/gate/gatetest`-style `gate/*test` package, so that each adapter runs it. Check: the in-memory fake and each store dialect run the suite from that package.
- [ ] 3.7 SECURITY. Make each error of `breakGlassKey` that is `ErrInvalid` a `RefusalError`, so that bad client input is not reported as gate.unavailable or as a server fault. Check: a test shows that a bad audit entry ID and a bad unit count return a `RefusalError` of the kind `ErrInvalid`.
