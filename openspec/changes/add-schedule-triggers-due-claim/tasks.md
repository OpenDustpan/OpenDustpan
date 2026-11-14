## 1. Failing tests first

- [ ] 1.1 Add conformance cases for ClaimDue: double claim, invalid slot, and collapse of missed slots. Check: TestClaimDueDoubleStart fails on main, passes after the change.
- [ ] 1.2 Extend existing Dispatcher.Scheduled with a case that returns ErrInvalid for a slot the frequency does not give. Check: TestDispatcherScheduledInvalidSlot fails on main, passes after the change.
- [ ] 1.3 Add new pkg/cron parser, Next, presets, and hash spread with unit tests. Check: TestSchedulePresetSpread shows hourly, every-6-hours, daily, and weekly with hash offsets.

## 2. Build

- [ ] 2.1 Implement ClaimDue on trigger.Store in each adapter as one conditional update. Check: `go test ./pkg/trigger/... ./pkg/store/...` passes.
- [ ] 2.2 Extend Dispatcher.Scheduled with cron Next check, last-saver read check, and source kind schedule. Check: `go test ./pkg/trigger/...` passes for two instances and one slot.
- [ ] 2.3 Wire the durable schedule trigger.schedule each minute to Dispatcher.Scheduled. Check: `go test ./internal/durable/...` passes for tick each minute and one catch-up run.
- [ ] 2.4 Add the docs page for schedule triggers: presets, custom syntax, UTC, one hour minimum, and spread. Check: `make docs-lint` passes for the new page.

## 3. Definition of done

- [ ] 3.1 Check wiring: routes, authorization actions, and the tick wiring to the job service. Check: Route test lists the trigger routes with their actions and the public tick has no auth bypass.
- [ ] 3.2 Add an e2e step with a real unit and a schedule trigger that fires one plan. Check: e2e step drives the real CLI: save a daily trigger, force the slot, and read one plan job for the unit.
- [ ] 3.3 Run a usability check of the docs page and fix each error text to name the one command that helps. Check: Persona follows the docs page alone from save to first scheduled plan.
- [ ] 3.4 Run make check with zero findings. Check: `make check` reports zero findings.
