## ADDED Requirements

### Requirement: New-unit default

When a unit is made, the server SHALL seed its sharing rules from the namespace setting outputSharing.newUnitDefault.

#### Scenario: Default nobody denies
- **WHEN** A job of unit B reads outputs of new unit A with the namespace default nobody and no rule.
- **THEN** The read is denied and the denial is written to the audit log.

#### Scenario: Namespace default allows
- **WHEN** The namespace sets outputSharing.newUnitDefault to namespace and a new unit is made.
- **THEN** The new unit holds one seeded rule that names the namespace ID and the read passes check 1 when check 2 also passes.

#### Scenario: Concurrent saves conflict
- **WHEN** Two administrators save outputSharing.newUnitDefault at the same time.
- **THEN** The second save answers 409 with error code namespace.settings-conflict and changes nothing.

### Requirement: One-step rules

When a sharing rule of a producer names a unit, a collection, or a namespace, the server SHALL pass check 1 for a job whose unit matches that target by stable ID.

#### Scenario: Collection rule shares within collection
- **WHEN** A collection rule shares outputs with that collection.
- **THEN** Each job of a member unit passes check 1 for outputs of each other member unit.

#### Scenario: Namespace rule shares within namespace
- **WHEN** A namespace rule shares outputs with that namespace.
- **THEN** Each job of a unit of the namespace passes check 1 for outputs of the producer.

#### Scenario: Invalid target rejected
- **WHEN** A rule names an unknown object or an object of another namespace.
- **THEN** The save answers 400 with error code output-sharing.bad-target and stores no rule.

#### Scenario: Rename keeps sharing by ID
- **WHEN** A collection with a sharing rule is renamed or removed and made again.
- **THEN** The rule still matches only members of the renamed collection, and a new collection with the old name gets no access.

### Requirement: Rule-write actions

When a principal writes a sharing rule on a producer, the server SHALL require unit:manage-sharing on the producer.

#### Scenario: Rule write needs sharing action
- **WHEN** A principal without unit:manage-sharing on the producer writes a sharing rule.
- **THEN** The write is denied with 403 when the principal misses the action.

### Requirement: Default-change action

When a principal changes outputSharing.newUnitDefault, the server SHALL require namespace:manage-settings on the namespace.

#### Scenario: Default change needs settings action
- **WHEN** A principal without namespace:manage-settings changes outputSharing.newUnitDefault.
- **THEN** The change is denied with 403 when the principal misses the action.

### Requirement: Read check

When a job token reads root outputs of another unit, the server SHALL allow the read only when a sharing rule names the job unit and the starter holds unit:read-outputs on the producer.

#### Scenario: Both tool paths checked
- **WHEN** A job token reads outputs through a Pulumi StackReference or an OpenTofu remote state read.
- **THEN** The read is allowed only when a naming rule exists and the starter holds unit:read-outputs on the producer.

#### Scenario: Tool-neutral and TFE output routes checked
- **WHEN** A caller reads the tool-neutral outputs route or a TFE-compatible output route of the producer.
- **THEN** The read is allowed only with the same naming rule and the same starter grant.

#### Scenario: Rule removed mid-job
- **WHEN** A rule is removed after the start of the job.
- **THEN** The next read is denied.

#### Scenario: Grant removed mid-job
- **WHEN** The starter loses unit:read-outputs on the producer after the start of the job.
- **THEN** The next read is denied.

#### Scenario: Cross-namespace read denied
- **WHEN** A job whose unit is in another namespace reads outputs of the producer.
- **THEN** The read is denied even when a collection rule names the job unit.

#### Scenario: In-flight read fails closed
- **WHEN** A rule removal races a read that already started.
- **THEN** The in-flight read is denied when either check no longer passes at read time.

#### Scenario: Secret output needs secrets grant
- **WHEN** A job token reads secret outputs of another unit with only unit:read-outputs.
- **THEN** The read of each secret output is denied without unit:read-secrets on the producer.

#### Scenario: Trigger saver used
- **WHEN** A trigger-started job reads outputs of another unit.
- **THEN** The read is denied when the saver is missing, deleted, or disabled.

### Requirement: Stale targets

Where a sharing rule names a deleted object, the server SHALL deny each read that relies only on that rule.

#### Scenario: Deleted target denies
- **WHEN** A rule names a unit or collection that is then deleted.
- **THEN** Each later read that relies only on that rule is denied.

### Requirement: Audit of sharing

When a sharing rule or the new-unit default changes, the server SHALL write an audit entry with the actor and the change.

#### Scenario: Rule change audited
- **WHEN** A sharing rule is made or removed or the namespace default changes.
- **THEN** The audit log holds one entry with the actor, the producer, the target, and the time.

#### Scenario: Denied read audited
- **WHEN** An output read is denied for a missing rule or a missing grant.
- **THEN** The audit log holds one entry with the actor, the producer, the consumer unit, and the time.
