## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for the NEW Fake Advance, Reset and Stop scenarios and for the NEW TestLeaseMargin in pkg/durable Check: `go test ./internal/clock/... ./internal/clocktest/...` on the NEW packages fails on main and the NEW TestLeaseMargin in pkg/durable fails on main

## 2. Build

- [ ] 2.1 Add NEW internal/clock with Clock and Real, and NEW internal/clocktest with Fake and Advance Check: `go test ./internal/clock/... ./internal/clocktest/...` on the NEW packages passes
- [ ] 2.2 Convert pkg/durable runner lease, claim time and deadline timers to the shared clock Check: `rg -n time.Now pkg/durable` on the converted pkg/durable returns nothing except clock wiring

## 3. Definition of done

- [ ] 3.1 Add forbidigo bans for direct wall-clock calls in pkg and internal except the NEW clock packages, cmd and listed exclusions Check: `make lint` passes and a trial direct call fails forbidigo in a converted package
- [ ] 3.2 Write the NEW conversion recipe docs page for embedders Check: `make docs-lint` passes for the NEW conversion recipe page
- [ ] 3.3 Wire clock.Real{} in pkg/dustpan and run the full check with usability from the recipe alone. Confirmed by: the driver's gates. Check: `make check` has zero findings
