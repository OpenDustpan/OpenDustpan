## ADDED Requirements

### Requirement: Claim one slot once

When two instances claim the same slot, the server SHALL start one plan job for that slot.

#### Scenario: Two instances race
- **WHEN** two instances call ClaimDue for the same trigger and slot at the same time
- **THEN** one claim succeeds and starts one plan job, and the other starts nothing

### Requirement: Catch up after outage

When the tick resumes after missed periods, the server SHALL start one plan job for the latest missed slot.

#### Scenario: Latest missed slot only
- **WHEN** the tick resumes after missed periods
- **THEN** one plan job starts for the latest missed slot and none for older slots

### Requirement: Refuse foreign slot

If a slot is not a time of the frequency, then the server SHALL refuse the slot.

#### Scenario: Foreign slot refused
- **WHEN** a caller gives a slot that the frequency does not give
- **THEN** ClaimDue claims nothing and returns an error, and Dispatcher.Scheduled returns ErrInvalid

### Requirement: Refuse short frequency

If a frequency is below one hour or never runs, then the server SHALL refuse it at save.

#### Scenario: Short frequency refused
- **WHEN** a person saves a frequency below one hour or an expression that never runs
- **THEN** the save is refused with an error that names the one hour minimum

### Requirement: Record schedule source

When a schedule trigger starts a job, the server SHALL record the source kind schedule on the job.

#### Scenario: Source kind
- **WHEN** a schedule trigger starts a plan
- **THEN** the job shows the source kind schedule

### Requirement: Start as service principal

When a schedule trigger starts a plan, the server SHALL run the start as the namespace service principal.

#### Scenario: Scheduled plan principal
- **WHEN** a schedule trigger starts a plan
- **THEN** the job records the namespace service principal

#### Scenario: Missing service grant
- **WHEN** the stored grant of the namespace service principal is missing at tick time
- **THEN** the tick starts nothing and writes a record that names the missing grant

### Requirement: Claim without job

When a claim does not lead to a job, the server SHALL return the slot to due on lease expiry with a record that names the slot and the cause.

#### Scenario: Start fails after claim
- **WHEN** ClaimDue succeeds but the job start is refused
- **THEN** the server writes a record, the lease expires after 2 minutes and the next tick retries the slot

### Requirement: Advance next slot

When a claimed slot starts a job, the server SHALL advance the stored next slot to the next time of the frequency.

#### Scenario: Advance on success
- **WHEN** a claimed slot starts a job
- **THEN** the next slot becomes Next of the claimed slot

### Requirement: Pause on repeated failure

When a trigger fails to start 5 times in a row, the server SHALL pause the trigger and send a notification.

#### Scenario: Five failures pause
- **WHEN** the start is refused 5 times in a row
- **THEN** the trigger pauses, sends a notification and starts nothing until a person resumes it

### Requirement: First slot uses spread

When a person saves or enables a schedule trigger, the server SHALL set the first slot by the hash spread inside the period.

#### Scenario: First slot offset
- **WHEN** a person saves a schedule trigger
- **THEN** the next slot is the first frequency time at or after save plus the hash offset

### Requirement: Re-enabled trigger recomputes

When a person re-enables a disabled trigger, the server SHALL recompute the next slot from the enable time.

#### Scenario: Re-enable
- **WHEN** a person re-enables a disabled trigger
- **THEN** no catch-up starts for the disabled period

### Requirement: Tick idempotence

When two ticks carry the same slot, the server SHALL start at most one plan job for that slot.

#### Scenario: Duplicate tick
- **WHEN** two ticks carry the same slot, one of them up to 2 minutes late
- **THEN** one claim succeeds and the duplicate starts nothing

### Requirement: Save authorization

When a person saves a schedule trigger, the server SHALL check the settings action on the unit.

#### Scenario: Save check
- **WHEN** a person without the settings action saves a trigger
- **THEN** the save is refused

### Requirement: Stable unit reference

When a unit is renamed, the server SHALL keep the trigger on the stable unit ID.

#### Scenario: Renamed unit
- **WHEN** a unit is renamed
- **THEN** the trigger still starts plans for that unit

### Requirement: Scheduled write gate

When a scheduled job holds a phase that writes state, the server SHALL hold the job for approval under the built-in schedule gate.

#### Scenario: Write phase waits
- **WHEN** a scheduled job holds a phase that writes state
- **THEN** the job waits for approval

### Requirement: Last saver read rights

When the last saver lacks unit:read-outputs at tick time, the server SHALL refuse the scheduled start.

#### Scenario: Rights lost
- **WHEN** the last saver no longer holds unit:read-outputs
- **THEN** the tick starts nothing and writes a denial record that names the saver and the action

### Requirement: Re-save race

When a frequency is re-saved before a pending claim commits, the server SHALL refuse the stale claim.

#### Scenario: Save before claim
- **WHEN** a person re-saves the frequency after the tick read the old slot
- **THEN** the stale claim starts nothing and a record names the old and new slot
