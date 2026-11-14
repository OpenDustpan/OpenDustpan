## ADDED Requirements

### Requirement: effective-schedule

The server SHALL compute the effective schedule of a unit in the order unit, collection, namespace, with off when no level has a setting.

#### Scenario: Off by default
- **WHEN** no level has a setting and EffectivePolicy runs
- **THEN** the result is off and no drift job starts.

#### Scenario: Unit overrides
- **WHEN** the namespace is on and the unit is off
- **THEN** EffectivePolicy returns off for the unit.

#### Scenario: Delete falls back
- **WHEN** the unit setting is deleted and the collection is on
- **THEN** EffectivePolicy returns the collection value.

#### Scenario: No collection
- **WHEN** the unit is in no collection and the namespace is on
- **THEN** EffectivePolicy returns the namespace value.

### Requirement: set-schedule

When a principal with the settings action of the object sets a schedule, the server SHALL store a preset or a 5-field UTC cron text with a gap of at least 1 hour.

#### Scenario: Hourly
- **WHEN** the principal sets hourly on a unit
- **THEN** the unit gets scheduled checks at its hashed offset.

#### Scenario: Bad cron
- **WHEN** the text has a gap under 1 hour
- **THEN** the answer is 400 with drift.invalid-schedule.

#### Scenario: Long cron
- **WHEN** the text has more than 256 bytes
- **THEN** the answer is 400 with drift.invalid-schedule.

#### Scenario: No action
- **WHEN** the caller has no settings action
- **THEN** the answer is 403 with drift.schedule-forbidden.

#### Scenario: No setting
- **WHEN** the caller deletes a level that has no setting
- **THEN** the answer is 404 with drift.schedule-not-found.

### Requirement: one-start

When two server processes claim one slot, the server SHALL start one job.

#### Scenario: Double claim
- **WHEN** two processes fire one slot of a unit
- **THEN** one job with the source kind schedule exists.

### Requirement: no-approval

When a scheduled check runs, the server SHALL start the job with no approval wait.

#### Scenario: Plan only
- **WHEN** a scheduled check starts
- **THEN** the job has the operation refresh and never enters awaiting-approval.

### Requirement: tick-limit

While a tick has more than 100 due checks in one namespace, the server SHALL start 100 of them.

#### Scenario: Over the limit
- **WHEN** 150 checks are due in one namespace
- **THEN** 100 start and 50 start at the next tick.

### Requirement: pause

If 5 scheduled checks of a unit fail in a row, then the server SHALL pause the schedule of that unit.

#### Scenario: Five failures
- **WHEN** 5 scheduled checks fail in a row
- **THEN** Scheduled starts no more checks and the effective read shows paused.

#### Scenario: Restart
- **WHEN** the server restarts after the pause
- **THEN** the schedule is still paused.

#### Scenario: Clean check
- **WHEN** a check ends clean after 4 failures
- **THEN** the count is 0.

#### Scenario: Lock refusal
- **WHEN** a manual lock refuses the start
- **THEN** the count does not change.

#### Scenario: Resume
- **WHEN** a principal sets the schedule of the paused unit
- **THEN** the count resets and checks start again.

### Requirement: pause-notification

When the server pauses a schedule, the server SHALL send one notification of the type unit.drift-schedule-paused that names the unit.

#### Scenario: Pause notice
- **WHEN** the fifth failure pauses a schedule
- **THEN** one notification of the type unit.drift-schedule-paused exists for the unit.

### Requirement: object-delete

When an object that has a schedule is deleted, the server SHALL delete its schedule row in the same transaction.

#### Scenario: Unit deleted
- **WHEN** a unit with a schedule is deleted
- **THEN** no schedule row for its ID remains.
