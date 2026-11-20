## ADDED Requirements

### Requirement: New-unit rule

When the setting of a namespace is `namespace` and a stack or a workspace is made in it, the server SHALL store one sharing rule on the new unit that names the namespace of the unit, in the transaction that makes the unit. When the setting is `nobody`, the server SHALL store no rule.

#### Scenario: Default nobody gives no rule
- **WHEN** a unit is made in a namespace that never set the setting
- **THEN** the unit has no sharing rule.

#### Scenario: Namespace gives one rule
- **WHEN** an administrator sets the setting to `namespace` and a stack or a workspace is made
- **THEN** the unit has one rule that names its namespace, and the audit log holds the entry of that rule.

#### Scenario: Fault before the commit
- **WHEN** the store fails after the rule write and before the commit
- **THEN** no unit, no rule and no audit entry exists.

#### Scenario: Default change keeps units
- **WHEN** an administrator changes the setting
- **THEN** the units that exist keep their rules, and only later units use the new value.

### Requirement: Rules at unit, collection and namespace level

When a caller with `unit:manage-sharing` on a unit, a collection or a namespace saves a rule, the server SHALL store it by stable ID and answer 201 with the rule.

#### Scenario: Collection rule
- **WHEN** an administrator saves a rule on a producer that names a collection of the namespace
- **THEN** the rule is stored with the stable ID of the collection and `Find` matches each unit that the collection holds.

#### Scenario: Invalid target
- **WHEN** a rule names a principal, a whole other namespace, an unknown unit or collection, or a stable ID that belongs to another object
- **THEN** the save answers 400 or 404 and stores no rule.

#### Scenario: Collection made again
- **WHEN** a collection of the producer namespace is removed and made again with the same name
- **THEN** the new collection does not match the old rule.

#### Scenario: Consumer in another namespace
- **WHEN** a rule names a unit or a collection of another namespace
- **THEN** the store keeps the reference by name, and the answer is the same whether or not that object exists.

#### Scenario: Duplicate and limit
- **WHEN** a rule with the same producer and consumer exists, or the producer has 100 rules
- **THEN** the save answers 409 and stores nothing.

#### Scenario: Caller without the action
- **WHEN** a caller holds `unit:read-access` and not `unit:manage-sharing`
- **THEN** the save and the removal answer 403.

### Requirement: Rule list and removal

When a caller with `unit:read-access` on a unit lists its rules, the server SHALL answer the rules whose producer is that unit, ordered by ID. When a caller with `unit:manage-sharing` removes a rule, the server SHALL remove it only when its producer is the object of the route.

#### Scenario: Removal on another producer
- **WHEN** a caller removes a rule by the ID of a rule of another producer
- **THEN** the answer is 404 and the rule stays.

#### Scenario: Purge
- **WHEN** a unit is purged
- **THEN** the rules that name it as producer or as consumer in its namespace are removed.

### Requirement: Rule audit

When a rule is saved or removed, the server SHALL append one audit entry with the actor, the producer, the consumer, the action and the rule ID in the transaction of the write.

#### Scenario: Rule change audited
- **WHEN** an administrator saves and removes a rule
- **THEN** the audit log holds one entry for each change.

#### Scenario: Audit append fails
- **WHEN** the append of the entry fails
- **THEN** the rule is not stored or not removed.
