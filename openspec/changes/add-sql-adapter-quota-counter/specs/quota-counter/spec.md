## ADDED Requirements

### Requirement: reserve-within-limit

When Reserve runs for a key within max, the server SHALL store the reservation in the same transaction as the guarded write.

#### Scenario: reserve over max is refused
- **WHEN** Reserve runs when usage plus one exceeds max
- **THEN** the server returns quota.ErrExceeded and stores no reservation

#### Scenario: first use of a key succeeds
- **WHEN** Reserve runs for a key with no stored count and max at least one
- **THEN** the server creates the key row and stores the reservation

#### Scenario: duplicate reservation retry keeps one row
- **WHEN** Reserve repeats with the same reservation ID after a client retry
- **THEN** the server keeps one reservation and reports success

### Requirement: atomic-record

When Record runs with checks where one requests limit fails, the server SHALL store no submit for any key of the checks.

#### Scenario: one failing check stores nothing
- **WHEN** Record runs with checks where one requests limit fails
- **THEN** the server stores no submit for any key

### Requirement: requests-window

When Record runs, the server SHALL count only submits inside the last window of each requests rule.

#### Scenario: old submits stop counting
- **WHEN** Record runs after submits older than the window filled the limit
- **THEN** Record succeeds and stores one new submit for the key

#### Scenario: in-window submits count
- **WHEN** Record runs while in-window submits already reach max
- **THEN** the server returns quota.ErrExceeded and stores no submit

#### Scenario: expired reservations leave with Reserve
- **WHEN** Reserve runs while expired reservations exist for the key
- **THEN** usage excludes them and no expired row remains for the counted keys

### Requirement: concurrent-reserve

While two Reserve calls run at the same time on one key, the server SHALL keep the stored count at or below max.

#### Scenario: two concurrent reserves keep max
- **WHEN** two Reserve calls run at the same time on one key at max minus one
- **THEN** at most max reservations exist and the loser gets quota.ErrExceeded

#### Scenario: concurrent multi-key reserves within limit both succeed
- **WHEN** two reserves run at the same time on the same two keys in opposite order within limit
- **THEN** both reserves succeed with no deadlock and with each key stored once

#### Scenario: concurrent multi-key reserves over limit refuse one
- **WHEN** two reserves run at the same time on the same two keys where only one fits
- **THEN** one reserve succeeds and the other gets quota.ErrExceeded with no partial key stored

### Requirement: fence-check

When the namespace fence is stale, the server SHALL refuse Reserve and Record without a store change.

#### Scenario: stale fence stores nothing
- **WHEN** Reserve or Record runs with a stale namespace fence
- **THEN** the server returns a fence error and stores no reservation or submit

### Requirement: reservation-lifecycle

When a reservation is released, expires, settles, or gets a new end time, the server SHALL update usage in the same transaction.

#### Scenario: released reservation frees the slot
- **WHEN** a reservation is released
- **THEN** usage drops by one and a repeat release succeeds without a change

#### Scenario: expired reservation does not count
- **WHEN** a reservation passes its endsAt
- **THEN** usage excludes it and Record and Reserve ignore it

#### Scenario: settled reservation moves to settled
- **WHEN** a revision ends applied
- **THEN** reserved usage drops by one, settled usage rises by one, and a repeat settle succeeds without a change

#### Scenario: set end moves expiry
- **WHEN** SetEnd runs for a reservation
- **THEN** the new endsAt controls expiry

### Requirement: rollback-stores-nothing

If the guarded transaction rolls back, then the server SHALL store no reservation or submit from it.

#### Scenario: rolled back reserve leaves no count
- **WHEN** the guarded transaction rolls back after Reserve
- **THEN** usage is unchanged and no reservation row remains
