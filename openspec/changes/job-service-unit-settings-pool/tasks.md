## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing planner tests for settings resolution, override authorization and missing location. Check: TestStartResolvesFromSettings fails on main
- [ ] 1.2 Write failing read-check tests for saver loss and manual-fire intersection. Check: TestTriggerOutputReadDeniesAfterSaverLoss fails on main

## 2. Build

- [ ] 2.1 Add location, pool, profile ID and steps to unit settings with validation. Check: go test ./pkg/unitsettings/...
- [ ] 2.2 Wire the saver check into the output read handlers. Check: go test ./pkg/dustpan

## 3. Definition of done

- [ ] 3.1 Add the e2e step, docs page and routes with actions. Check: make e2e step passes
- [ ] 3.2 Run the full check. Check: make check with zero findings
