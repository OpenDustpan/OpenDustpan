## ADDED Requirements

### Requirement: Pulumi stack placement

When the Pulumi CLI makes a stack, the server SHALL place the stack in the collection of the first matching placement rule.

#### Scenario: First matching rule wins
- **WHEN** the Pulumi CLI makes a stack whose project and stack name match two placement rules
- **THEN** the new stack exists in the collection of the first matching rule

#### Scenario: Catch-all rule last
- **WHEN** a namespace has a narrow rule and then a catch-all rule, and a stack matches both
- **THEN** the stack is placed by the narrow rule

### Requirement: Workspace placement

When OpenTofu makes a workspace, the server SHALL place the workspace in the collection that the project of its cloud block names.

#### Scenario: Project names a collection
- **WHEN** OpenTofu makes a workspace whose cloud block project names an existing collection
- **THEN** the new workspace exists in that collection

### Requirement: Default at namespace create

When an administrator makes a namespace, the server SHALL store a default collection and a catch-all placement rule in the create transaction.

#### Scenario: Namespace gets the default
- **WHEN** an administrator makes a namespace without the opt-out
- **THEN** the namespace holds a default collection and a catch-all rule that names it, and the audit log holds an entry for each

#### Scenario: Stack uses the default
- **WHEN** the Pulumi CLI makes a stack that only the catch-all rule matches
- **THEN** the stack exists in the default collection

### Requirement: Opt-out of the default

Where the namespace create request sets the opt-out, the server SHALL store neither a default collection nor a catch-all rule.

#### Scenario: Namespace with the opt-out
- **WHEN** an administrator makes a namespace with the opt-out
- **THEN** the namespace holds no collection and no placement rule

#### Scenario: Administrator deletes the default
- **WHEN** an administrator deletes the catch-all rule of a namespace
- **THEN** a new stack that no rule matches is refused

### Requirement: Refuse stack with no match

If no placement rule matches a new Pulumi stack, then the server SHALL refuse the create and tell the caller to add a placement rule.

#### Scenario: No rule matches
- **WHEN** the Pulumi CLI makes a stack that no rule matches, or the rule list is empty
- **THEN** the server answers 422, names the namespace and the unit, names the placement-rules list command, and no stack exists

### Requirement: Refuse workspace with no collection

If a new workspace names no existing collection, then the server SHALL refuse the create and tell the caller to set the project in the cloud block.

#### Scenario: Project missing or unknown
- **WHEN** OpenTofu makes a workspace with no project or an unknown project
- **THEN** the server answers 422, says to set project, to make the collection or to make the workspace through the API with a collection, names the collections list command, and no workspace exists

### Requirement: No implicit collection

If a CLI create finds no collection, then the server SHALL make neither a collection nor a unit.

#### Scenario: Refusal makes nothing
- **WHEN** a CLI create finds no collection
- **THEN** no collection and no unit exist after the request

### Requirement: Grant check on the chosen collection

If the caller holds no grant to make a unit in the chosen collection, then the server SHALL refuse the create with 403.

#### Scenario: Caller has no grant
- **WHEN** the chosen collection exists and the caller holds no grant to make a unit there
- **THEN** the server answers 403, names the collection and the rule or project that chose it, and no unit exists

### Requirement: Order of checks

If the caller holds no right to make a unit in the namespace, then the server SHALL answer 403 before it tells whether a collection exists.

#### Scenario: No right and no collection
- **WHEN** a caller with no right to make units in the namespace creates a stack that no rule matches
- **THEN** the server answers 403 and does not say that no rule matches

#### Scenario: Right to make but no read of the collection
- **WHEN** a caller can make units in the chosen collection but cannot read the collection
- **THEN** the create succeeds and the answer shows only the unit

### Requirement: Record of a refusal

When the server refuses a CLI create for placement, the server SHALL write an audit entry with the unit and the reason.

#### Scenario: Refusal is audited
- **WHEN** a CLI create is refused for placement
- **THEN** the audit log holds an entry with the namespace, the unit name and the reason

#### Scenario: Conflict is audited
- **WHEN** a create loses a same-name race
- **THEN** the audit log holds an entry for the losing create with the unit name and the conflict reason

### Requirement: Record of a placement

When the server places a CLI-made unit, the server SHALL write the audit entry of the placement in the transaction that stores the unit.

#### Scenario: Placement is audited
- **WHEN** the Pulumi CLI makes a stack that a rule places
- **THEN** the audit log holds an entry with the unit, the collection and the rule

#### Scenario: Audit write fails
- **WHEN** the audit write fails during a placement
- **THEN** the transaction rolls back and no unit exists

### Requirement: Record of a rule change

When an administrator saves, reorders or deletes a placement rule, the server SHALL write an audit entry with the rule and the actor in the same transaction.

#### Scenario: Rule change is audited
- **WHEN** an administrator reorders two placement rules
- **THEN** the audit log holds an entry with the rules and the actor

### Requirement: Rule validation at save

If a placement rule names a collection that does not exist, then the server SHALL refuse to save the rule.

#### Scenario: Rule names a missing collection
- **WHEN** an administrator saves a rule with a collection ID that does not exist
- **THEN** the server answers 422 and the rule is not stored

### Requirement: Pattern limits

If a stack-name pattern of a placement rule is longer than 128 bytes, holds a character that is not printable ASCII, or holds more than 4 wildcard characters, then the server SHALL refuse to save the rule.

#### Scenario: Hostile pattern
- **WHEN** an administrator saves a rule with a pattern of 129 bytes, a control character, or more than 4 wildcard characters
- **THEN** the server refuses the rule and stores nothing

### Requirement: Rule count limit

If a namespace holds 100 placement rules, then the server SHALL refuse to save one more rule.

#### Scenario: Rule list at the limit
- **WHEN** an administrator saves a rule in a namespace that holds 100 rules
- **THEN** the server answers 422 and the rule is not stored

#### Scenario: Rule list below the limit
- **WHEN** an administrator saves the 100th rule
- **THEN** the rule is stored

### Requirement: Rule list order

When a caller lists the placement rules, the server SHALL return the rules in match order with the collection ID of each rule.

#### Scenario: List shows order
- **WHEN** an administrator reorders two rules and lists the rules
- **THEN** the list shows the new order and the collection IDs

#### Scenario: Collection not readable
- **WHEN** a caller can list rules but cannot read a collection that a rule names
- **THEN** the list still shows the rule with its collection ID

### Requirement: Deleted collection during create

If the chosen collection is deleted before the unit is stored, then the server SHALL refuse the create with the missing-collection error.

#### Scenario: Collection deleted in flight
- **WHEN** a collection is deleted between the rule match and the unit insert
- **THEN** the server refuses the create and no unit exists

### Requirement: Rule that names a deleted collection

If the matching rule names a collection that was deleted after the rule was saved, then the server SHALL refuse the create with the missing-collection error.

#### Scenario: Stale rule
- **WHEN** a rule names a collection that an administrator deleted later and a CLI create matches the rule
- **THEN** the server answers 422 and names the rule, and no unit exists

### Requirement: Same-name creates

If two CLI creates of one unit name run at the same time, then the server SHALL store one unit and answer the other create with a conflict error.

#### Scenario: Concurrent Pulumi create
- **WHEN** two Pulumi CLI processes make the same stack at the same time
- **THEN** one stack exists and the other process gets a conflict error

#### Scenario: Pulumi and TFE race
- **WHEN** a Pulumi create and a TFE workspace create target the same unit name at the same time
- **THEN** one unit exists and the other create gets a conflict error

### Requirement: Reorder with a create

When a rule reorder commits while a create runs, the server SHALL place the unit by the rule order that the create read in its transaction.

#### Scenario: Reorder races a create
- **WHEN** a create reads the rules before a reorder commits
- **THEN** the unit is placed by the old order and the audit entry names the rule that chose

### Requirement: Existing units stay

When an administrator changes or deletes a placement rule, the server SHALL keep the collection of each existing unit.

#### Scenario: Rule change does not move
- **WHEN** an administrator changes the rule that placed a stack
- **THEN** the stack stays in its collection
