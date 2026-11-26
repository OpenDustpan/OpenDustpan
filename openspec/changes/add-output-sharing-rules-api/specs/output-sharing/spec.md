## ADDED Requirements

### Requirement: Rules at unit, collection and namespace level

When a caller with `unit:manage-sharing` on a unit, a collection or a namespace saves a rule, the server SHALL store it by stable ID and answer 201 with the rule.

#### Scenario: Collection rule
- **WHEN** an administrator saves a rule on a producer that names a collection of the namespace
- **THEN** the rule is stored with the stable ID of the collection and `Find` matches each unit that the collection holds.

#### Scenario: Invalid target
- **WHEN** a rule names a principal, a whole other namespace, an unknown unit or collection, or a stable ID that belongs to another object
- **THEN** the save answers 400 or 404 and stores no rule.

#### Scenario: Collection made again
- **WHEN** a collection is removed and made again with the same name, in any namespace
- **THEN** the new collection does not match the old rule, and the removal deleted the old rule.

#### Scenario: Consumer in another namespace
- **WHEN** a rule names a unit or a collection of another namespace
- **THEN** the store keeps the namespace and the stable ID that the caller gives, checks the format of the ID only, and the answer is the same whether or not that object exists.

#### Scenario: Consumer ID with a bad format
- **WHEN** a rule names a unit or a collection with an ID that is empty, too long, or has a byte outside a-z, 0-9, "-" and "_"
- **THEN** the save answers 400 and stores no rule.

#### Scenario: Rename and make again in another namespace
- **WHEN** the unit or the collection of another namespace that a rule names is renamed, or removed and made again with the same name
- **THEN** a rename keeps the rule, and the object made again has a new ID and does not match.

#### Scenario: Duplicate and limit
- **WHEN** a rule with the same producer and consumer exists, or the producer has 100 rules
- **THEN** the save answers 409 and stores nothing.

#### Scenario: Caller without the action
- **WHEN** a caller holds `unit:read-access` and not `unit:manage-sharing`
- **THEN** the save and the removal answer 403.

### Requirement: Rule list and removal

When a caller lists the rules of a producer, the server SHALL answer the rules whose producer is that object, ordered by ID, and SHALL need `unit:read-access` on a unit, `collection:read-access` on a collection and `namespace:read-access` on a namespace (owner, 2026-11-20). When a caller with `unit:manage-sharing` removes a rule, the server SHALL remove it only when its producer is the object of the route.

#### Scenario: Collection rules listed
- **WHEN** a caller with `collection:read-access` on a collection lists its rules
- **THEN** the answer holds the rules whose producer is that collection, ordered by ID, and no rule of a unit, of a sibling collection or of the namespace.

#### Scenario: Namespace rules listed
- **WHEN** a caller with `namespace:read-access` on a namespace lists its rules
- **THEN** the answer holds the rules whose producer is that namespace, ordered by ID.

#### Scenario: List with the wrong read action
- **WHEN** a caller holds `unit:read-access`, `collection:read-access` or `namespace:read-access` on another object kind than the object of the list route, or holds `unit:manage-sharing` only
- **THEN** the list answers 403 and names the action.

#### Scenario: Removal on another producer
- **WHEN** a caller removes a rule by the ID of a rule of another producer
- **THEN** the answer is 404 and the rule stays.

### Requirement: Rule audit

When a rule is saved or removed, the server SHALL append one audit entry with the actor, the producer, the consumer, the action and the rule ID in the transaction of the write.

#### Scenario: Rule change audited
- **WHEN** an administrator saves and removes a rule
- **THEN** the audit log holds one entry for each change.

#### Scenario: Audit append fails
- **WHEN** the append of the entry fails
- **THEN** the rule is not stored or not removed.
