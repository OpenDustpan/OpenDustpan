## ADDED Requirements

### Requirement: New-unit default

When a unit is made, the server SHALL seed its sharing rule from the namespace setting outputSharing.newUnitDefault.

#### Scenario: Default nobody denies
- **WHEN** A job of unit B reads outputs of new unit A with the default nobody.
- **THEN** The read is denied and the denial is audited.

#### Scenario: Default change keeps units
- **WHEN** An administrator changes the default.
- **THEN** Existing units keep their rules and only later units use the new default.

### Requirement: Scope rules

When a sharing rule of a producer names a collection or a namespace, the server SHALL pass check 1 for each job whose unit belongs to that target by stable ID.

#### Scenario: Collection rule
- **WHEN** A collection rule shares outputs with its members.
- **THEN** Each member unit passes check 1 for the producer.

#### Scenario: Invalid target
- **WHEN** A rule names an unknown object or an object of another namespace.
- **THEN** The save answers 400 and stores no rule.

#### Scenario: Collection re-created
- **WHEN** A collection is removed and made again with the same name.
- **THEN** The new collection gets no access from the old rule.

### Requirement: Read check

When a job token reads outputs of another unit, the server SHALL allow the read only when a rule names the job unit and the starter holds unit:read-outputs on the producer.

#### Scenario: Rule removed mid-job
- **WHEN** A rule is removed after the job start.
- **THEN** The next read is denied.

#### Scenario: Trigger saver missing
- **WHEN** A trigger-started job reads outputs and the last saver is missing.
- **THEN** The read is denied.

### Requirement: Audit

When a sharing rule or the default changes, the server SHALL write an audit entry with the actor and the change.

#### Scenario: Rule change audited
- **WHEN** An administrator makes or removes a rule.
- **THEN** The audit log holds one entry with actor, producer, target and time.
