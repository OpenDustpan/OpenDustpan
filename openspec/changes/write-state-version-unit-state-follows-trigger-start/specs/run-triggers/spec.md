## ADDED Requirements

### Requirement: Start follows plan on final version

When the server records a final state version of a source unit, the server SHALL start a plan job with source kind trigger for each follows trigger whose source is that unit and whose when is applied.

#### Scenario: OpenTofu apply starts follower plan
- **WHEN** unit A writes a final state version and unit B has a follows trigger on A with when applied
- **THEN** the server starts a plan job with source kind trigger for unit B, once, and writes an audit entry for the automatic start.

#### Scenario: Plan without final version starts nothing
- **WHEN** an OpenTofu plan writes no final state version
- **THEN** the server starts no follower plan.

### Requirement: One run per version

When the same final version event arrives again on the intake path, the server SHALL start nothing new.

#### Scenario: Duplicate intake event returns first job
- **WHEN** the same final version event arrives a second time on the intake path
- **THEN** the server starts no new job and returns the first job.

### Requirement: Older version is refused

If an event names a version older than the last final version recorded for the unit, then the server SHALL return ErrVersionMismatch and start nothing.

#### Scenario: Older version is refused
- **WHEN** an event names a version older than the last final version recorded for the unit
- **THEN** the server returns ErrVersionMismatch and starts nothing.

#### Scenario: Concurrent older arrival is refused
- **WHEN** a newer version already fired and an older version arrives later
- **THEN** the server returns ErrVersionMismatch for the older event with no backfill.

### Requirement: Manual refire after missed event

When a caller fires a final state version by hand through the manual fire route, the server SHALL start one plan per matching follows trigger unless that version already fired.

#### Scenario: Refire of unfired version starts one plan per follower
- **WHEN** a caller fires a final version that no follower processed yet
- **THEN** the server starts one plan job per matching follows trigger for that version.

#### Scenario: Refire of fired version is refused
- **WHEN** a caller refires a version through the manual fire route that the followers already processed
- **THEN** the server starts nothing new and answers 409 with code trigger.version-mismatch.

### Requirement: Per-trigger errors do not stop others

When the job service refuses the plan of one follower, the server SHALL record the error on that trigger and continue with the other followers.

#### Scenario: One refusal leaves others running
- **WHEN** the job service refuses the plan for one follower of a source
- **THEN** the server stores the error on that trigger row and still starts the plans of the other followers.

### Requirement: Crash replay is safe

When the sender retries an event after a crash, the server SHALL replay the same per-trigger idempotency key and start nothing new.

#### Scenario: Retry after start starts nothing new
- **WHEN** the sender retries an event whose follower plans already started
- **THEN** the server returns the first job and starts nothing new.

### Requirement: Starting principal

When a follows trigger starts a plan, the server SHALL start it as the namespace service principal under its stored plan-start grant.

#### Scenario: Follows plan starts as service principal
- **WHEN** a follows trigger starts a plan
- **THEN** the start check uses only the stored plan-start grant of the namespace service principal.

### Requirement: Saver read check fails closed

When a plan that a follows trigger started reads an output of the source unit, the server SHALL allow the read only when the current grants of the last saver of the trigger hold unit:read-outputs on the source, plus unit:read-secrets for a secret output.

#### Scenario: Missing saver reads nothing
- **WHEN** a follows plan reads a source output while the last saver is missing, deleted, or disabled
- **THEN** the server denies the read and writes an audit entry for the denial.

#### Scenario: Secret output needs secrets action
- **WHEN** a follows plan reads a secret output of the source
- **THEN** the server denies the read without unit:read-secrets.

#### Scenario: Saver loses grant after save
- **WHEN** a follows plan reads a source output after the last saver lost unit:read-outputs
- **THEN** the server denies the read.

### Requirement: Version ID charset

When an upstream event carries a version ID, the server SHALL accept only bytes a-z, A-Z, 0-9, dash, underscore, and dot.

#### Scenario: Hostile version ID is refused
- **WHEN** an event carries a version ID with a byte outside a-z, A-Z, 0-9, dash, underscore, and dot
- **THEN** the server rejects the event without a plan start.

### Requirement: Declared follower edge

When a follows trigger exists, the server SHALL expose a declared dependency edge from the source unit to the follower unit.

#### Scenario: Graph shows follower edge
- **WHEN** a follows trigger exists on unit B with source unit A
- **THEN** the graph shows a declared edge from the source to the follower and safe delete of the source names the follower.

### Requirement: Delete past follower edge

When a source unit has a follower edge, the server SHALL refuse its normal delete and allow only a forced delete.

#### Scenario: Normal delete is refused
- **WHEN** a caller deletes a source unit that has a follower edge
- **THEN** the server refuses the delete and names the follower.

#### Scenario: Forced delete passes
- **WHEN** a caller forces the delete of a source unit that has a follower edge
- **THEN** the server deletes the source.

### Requirement: Cycle refused at save

When a trigger save makes a follows cycle, the server SHALL refuse the save and name the loop path.

#### Scenario: Cycle save names path
- **WHEN** a trigger save makes a follows cycle
- **THEN** the server refuses the save and names the units of the loop in order.

### Requirement: Follower count bounded at save

When a source unit already has 50 followers, the server SHALL refuse a further follows trigger on that source.

#### Scenario: Fifty-first follower is refused
- **WHEN** a source already has 50 followers
- **THEN** the server refuses the save.

### Requirement: Trigger route authorization

When a caller calls a trigger route, the server SHALL require unit:manage-triggers on the unit.

#### Scenario: Caller without action is denied
- **WHEN** a caller without unit:manage-triggers calls a trigger route
- **THEN** the server denies the call with 403.

### Requirement: Invalid stored row starts nothing

When a stored follows trigger row fails validation, the server SHALL start no plan for that row.

#### Scenario: Stored row fails validation
- **WHEN** a stored follows trigger row no longer validates
- **THEN** the server starts no plan for that row.

#### Scenario: Trigger deleted before fan-out starts nothing
- **WHEN** a trigger is deleted between the version write and the fan-out
- **THEN** the server starts no plan for the deleted trigger.

#### Scenario: Cross-namespace source starts nothing
- **WHEN** a follows trigger names a source unit in another namespace
- **THEN** the server refuses the trigger save.
