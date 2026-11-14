## ADDED Requirements

### Requirement: Claim one slot once

When two instances claim the same slot, the server SHALL start one plan job for that slot.

#### Scenario: Two instances race for one slot
- **WHEN** two instances call ClaimDue for the same trigger and slot at the same time
- **THEN** one claim succeeds and starts one plan job, and the other claim starts nothing

### Requirement: Catch up after outage

When the tick resumes after missed periods, the server SHALL start one plan job for the latest missed slot.

#### Scenario: Latest missed slot only
- **WHEN** the tick resumes after missed periods
- **THEN** one plan job starts for the latest missed slot and no job starts for older missed slots

### Requirement: Refuse foreign slot

If a slot is not a time of the frequency, then the server SHALL refuse the slot.

#### Scenario: Foreign slot refused
- **WHEN** the caller gives a slot that the frequency does not give
- **THEN** ClaimDue claims nothing and returns an error, and Dispatcher.Scheduled returns ErrInvalid

### Requirement: Refuse short frequency

If a frequency is below one hour or never runs, then the server SHALL refuse it at save.

#### Scenario: Short frequency refused at save
- **WHEN** a person saves a frequency below one hour or an expression that never runs
- **THEN** the save is refused with an error that names the one hour minimum

### Requirement: Record schedule source

When a schedule trigger starts a job, the server SHALL record the source kind schedule on the job.

#### Scenario: Scheduled job source kind
- **WHEN** a schedule trigger starts a plan
- **THEN** the job shows the source kind schedule

### Requirement: Start as service principal

When a schedule trigger starts a plan, the server SHALL run the start as the namespace service principal.

#### Scenario: Scheduled plan principal
- **WHEN** a schedule trigger starts a plan
- **THEN** the job records the namespace service principal under its stored grant for plan start only

#### Scenario: Missing service grant at tick
- **WHEN** the stored grant of the namespace service principal is revoked or missing at tick time
- **THEN** the tick starts nothing and writes a record that names the missing grant

### Requirement: Claim without job

When a claim does not lead to a job, the server SHALL keep the slot due for retry at the next tick with a record that names the slot and the cause.

#### Scenario: Claim then start fails
- **WHEN** ClaimDue succeeds but the job start is refused or the instance stops before the start
- **THEN** the server writes a record that names the slot and the cause and keeps the same slot due, and the next tick retries that slot

### Requirement: Trigger authorization

When a person saves a schedule trigger, the server SHALL check the settings action on the unit.

#### Scenario: Save and tick checks
- **WHEN** a person saves a trigger and the tick starts a plan
- **THEN** the save needs the settings action on the unit and the tick needs the stored grant of the namespace service principal

### Requirement: Stable unit reference

When a unit is renamed, the server SHALL keep the trigger on the stable unit ID.

#### Scenario: Renamed or disabled unit
- **WHEN** a unit is renamed, disabled, or removed
- **THEN** the trigger follows the stable unit ID, a disabled trigger starts nothing, and a removed unit starts nothing

### Requirement: Scheduled write gate

When a scheduled job holds a phase that writes state, the server SHALL hold the job for approval under the built-in schedule gate.

#### Scenario: Scheduled apply phase waits
- **WHEN** a scheduled job holds a phase that writes state or changes resources
- **THEN** the job waits for approval under the built-in schedule gate as any scheduled job waits

### Requirement: Last saver read rights

When the last saver lacks unit:read-outputs at tick time, the server SHALL refuse the scheduled start.

#### Scenario: Last saver loses read rights
- **WHEN** the last saver no longer holds unit:read-outputs on the producer at tick time
- **THEN** the tick starts nothing and writes a denial record that names the last saver and the missing action

### Requirement: Re-save race with claim

When a frequency is re-saved before a pending claim commits, the server SHALL refuse the stale claim.

#### Scenario: Save between tick read and claim
- **WHEN** a person re-saves the frequency after the tick read the old slot but before the conditional claim commits
- **THEN** the stale claim starts nothing and the server writes a record with the old and new slot
