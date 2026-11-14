## Context

Follows triggers fan out from one source and chain unit to unit. The store serializes trigger writes per namespace in one transaction so two saves check current rows, and the dispatcher carries a depth counter in the job origin so a loop made outside a save still stops.

## Decisions

1. **Caps and cycle check at save.** Check both caps inside trigger.Store.Put in one serialized namespace transaction: count triggers of the unit and followers of the source, then check the cycle, then write. Read both caps from the installation settings inside Put at each save with no cached copy, so an administrator change applies to the next save. Existing rows above a lowered cap stay; only a save that adds above the new cap is refused.
2. **Depth counter in job origin.** Carry depth in the job origin: a trigger start sets depth to parent depth plus one, a missing origin starts at zero, and a manual fire resets to zero. The dispatcher refuses above depth 10, writes one audit entry for the refused start, writes one log line that names the loop, and keeps the start refused when the audit write fails.

## Structure

Unexported triggerStore with constructor newTriggerStore that holds no cap copy; only Put writes a trigger, and Put reads caps from settings, holds the per-namespace serialized transaction, then checks caps and cycle before the write.

## Risks

- A concurrent save skips the cycle check when the adapter does not serialize writes of one namespace.
- A changed cap does not apply because the old value is cached past the save.
