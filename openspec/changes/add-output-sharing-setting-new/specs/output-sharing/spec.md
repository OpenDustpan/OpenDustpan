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

### Requirement: Rules of a purged object

When a unit is purged or a collection is deleted, the server SHALL remove, in the same transaction, each rule that names the stable ID of that object, and SHALL list the removed rules in the audit entry of the purge or of the delete.

#### Scenario: Purge
- **WHEN** a unit is purged, or a collection is deleted
- **THEN** each rule that names its namespace and stable ID as producer or as consumer, in any producer namespace, is removed; a rule that names the same ID in another namespace stays in the same transaction, and the audit entry of the purge lists the removed rules.
