## ADDED Requirements

### Requirement: Show schedule next run

When a trigger record is read, the server SHALL include the evaluated trigger path and the schedule next run time.

#### Scenario: Read trigger shows next run
- **WHEN** a schedule trigger that can fire is read through getTrigger or listUnitTriggers
- **THEN** the answer includes the evaluated trigger path computed at read time from the stored trigger record and the schedule next run time in Unix milliseconds.

#### Scenario: Unreadable schedule returns null
- **WHEN** a trigger with no schedule, a disabled trigger, or a schedule that can never fire is read
- **THEN** the answer returns null for schedule next run time and still returns the evaluated trigger path.

### Requirement: Record skipped slots

When a schedule slot reaches its time and does not start a plan, the server SHALL write one audit entry with server actor, trigger ID, slot time, and one allowlisted reason.

#### Scenario: Skipped slot is recorded
- **WHEN** a schedule slot reaches its time and does not start a plan
- **THEN** an audit entry exists with server actor, trigger ID, slot time in Unix milliseconds, and one reason of lock-held, plan-running, trigger-disabled, or missed-during-downtime.

#### Scenario: Downtime catch-up is bounded
- **WHEN** the server restarts after downtime with missed slots
- **THEN** one entry per missed slot keyed by trigger ID and slot time exists and the audit write never blocks the scheduler decision.

### Requirement: Record replaced plans

When a stored plan is replaced before it runs, the server SHALL write one audit entry with server actor, trigger ID, replaced plan ID, and reason.

#### Scenario: Replaced plan is recorded
- **WHEN** a stored plan is replaced before it runs
- **THEN** an audit entry exists with server actor, trigger ID, replaced plan ID, and reason replaced-by-newer-plan.

#### Scenario: Manual fire that replaces is recorded
- **WHEN** a manual fire replaces a stored plan before it runs
- **THEN** an audit entry exists with the caller as actor for the fire and the server as actor for the replaced plan.

#### Scenario: Removed trigger keeps reference
- **WHEN** a trigger is removed and made again
- **THEN** each earlier entry still references the stable trigger ID.
