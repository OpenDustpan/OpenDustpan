## Context

Schedule triggers start plans without a person. Several server processes share one store. The design must make a double start in one period impossible by structure, and must collapse missed slots after an outage into one run. Dispatcher.Scheduled already exists with a conformance harness; this change extends it with the slot check. The cron package is new work in this change. This change owns the trigger.schedule tick wiring; run-triggers owns the trigger record and the push and follows starts.

## Decisions

1. **One conditional claim owns the slot.** Add ClaimDue to trigger.Store as one conditional store update that claims one slot for one instance. The row holds the next slot. The update succeeds for one caller only. Missed slots collapse to the latest missed slot before the claim. A frequency edit recomputes the next slot from the save time and writes a record of the old and new slot.
2. **Extend Dispatcher with slot and read checks.** Extend the existing Dispatcher.Scheduled over the store from run-triggers. It checks the slot against cron Next before the claim and returns ErrInvalid for a slot the frequency does not give. It checks that the last saver holds unit:read-outputs and refuses the start when the check fails. The durable schedule trigger.schedule ticks each minute and calls it with the tick time as the single time source; instances never use their own clock for due.
3. **New cron package serves triggers.** Add new package pkg/cron for parse, Next, presets hourly, every-6-hours, daily, and weekly, and hash spread. Save refuses a frequency below one hour and an expression that never runs. The minimum interval is the drafter interpretation: the smallest gap between two consecutive Next times; a list or step with a sub-hour gap is refused.

## Structure

Unexported claim guard inside the trigger store adapter with constructor newScheduleClaim that checks the slot with cron Next before the conditional update. Only the constructor can build a claimed slot.

## Risks

- Two instances claim the same slot when the conditional update is not atomic.
- An outage causes a burst of catch-up runs when missed slots do not collapse.
- A wrong slot starts a job outside the frequency when the slot check is skipped.
