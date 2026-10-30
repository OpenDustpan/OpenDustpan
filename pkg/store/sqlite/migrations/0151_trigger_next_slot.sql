-- The next slot of a schedule trigger: the first run that a claim can still
-- take, in Unix milliseconds. Put sets it from the schedule when it makes or
-- switches on an enabled schedule trigger. The value 0 means that the row has
-- no slot, which holds for a trigger of another kind and for a trigger that is
-- off. An enabled schedule trigger with the value 0 is refused. A claim of a slot
-- moves the value to the slot after it, in one transaction, so of many instances
-- that claim one slot exactly one succeeds.
ALTER TABLE triggers ADD COLUMN next_slot_ms INTEGER NOT NULL DEFAULT 0
    CHECK (next_slot_ms >= 0 AND (kind <> 'schedule' OR enabled = 0 OR next_slot_ms > 0));
