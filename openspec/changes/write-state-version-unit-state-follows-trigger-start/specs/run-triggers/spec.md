## ADDED Requirements

### Requirement: Start follows plan on final version

When the server records a final state version of a source unit, the server SHALL start a plan job with source kind trigger for each follows trigger whose source is that unit and whose when is applied.

#### Scenario: OpenTofu apply starts follower plan
- **WHEN** unit A writes a final state version and unit B has a follows trigger on A
- **THEN** the server starts one plan job with source kind trigger for unit B.

#### Scenario: Plan without final version starts nothing
- **WHEN** an OpenTofu plan writes no final state version
- **THEN** the server starts no follower plan.

### Requirement: One run per version

When the same final version event arrives again, the server SHALL start no new job.

#### Scenario: Duplicate event
- **WHEN** the same event arrives a second time
- **THEN** the server starts no new job.

### Requirement: Older version is refused

If an event names a version older than the last final version of the unit, then the server SHALL return ErrVersionMismatch.

#### Scenario: Older version
- **WHEN** an event names an older version
- **THEN** the server returns ErrVersionMismatch and starts nothing.

### Requirement: Refire after a missed event

When a caller fires a final state version through the trigger fire route, the server SHALL start one plan for each matching follows trigger that did not process that version.

#### Scenario: Refire of unfired version
- **WHEN** a caller fires a version that no follower processed
- **THEN** the server starts one plan for each matching follower.

### Requirement: Per-trigger errors

When the job service refuses the plan of one follower, the server SHALL record the error on that trigger.

#### Scenario: One refusal
- **WHEN** the job service refuses one follower
- **THEN** the server stores the error on that trigger and starts the other followers.

### Requirement: Saver read check

While a plan started by a follows trigger reads a source output, the server SHALL allow the read only when the current grants of the last saver hold unit:read-outputs on the source.

#### Scenario: Missing saver
- **WHEN** the last saver is missing, deleted or disabled
- **THEN** the server denies the read and writes an audit entry.

#### Scenario: Secret output
- **WHEN** the plan reads a secret output and the saver lacks unit:read-secrets
- **THEN** the server denies the read.

### Requirement: Version ID charset

When an upstream event carries a version ID, the server SHALL accept only the bytes a-z, A-Z, 0-9, dash, underscore and dot.

#### Scenario: Hostile version ID
- **WHEN** an event carries another byte
- **THEN** the server rejects the event and starts no plan.

### Requirement: Declared follower edge

When a follows trigger exists, the server SHALL expose a declared dependency edge from the source unit to the follower unit.

#### Scenario: Graph shows edge
- **WHEN** unit B has a follows trigger on A
- **THEN** the graph shows a declared edge and a normal delete of A is refused.

#### Scenario: Forced delete
- **WHEN** a caller forces the delete of A
- **THEN** the server deletes A.

### Requirement: Invalid row

If a stored follows trigger row fails validation, then the server SHALL start no plan for that row.

#### Scenario: Row fails validation
- **WHEN** a stored row no longer validates
- **THEN** the server starts no plan for it.
