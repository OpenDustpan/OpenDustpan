## ADDED Requirements

### Requirement: Pulumi stack placement

When the Pulumi CLI makes a stack, the server SHALL place the stack in the collection of the first matching placement rule.

#### Scenario: First matching rule wins
- **WHEN** the Pulumi CLI makes a stack that matches two rules
- **THEN** the stack exists in the collection of the first rule

#### Scenario: Catch-all last
- **WHEN** a namespace has a narrow rule and then a catch-all rule and a stack matches both
- **THEN** the narrow rule places the stack

### Requirement: Workspace placement by project

When OpenTofu makes a workspace whose cloud block names a project, the server SHALL place the workspace in the collection that the project names.

#### Scenario: Project names a collection
- **WHEN** the project names an existing collection
- **THEN** the workspace exists in that collection

### Requirement: Workspace placement by rule

When OpenTofu makes a workspace whose cloud block names no project, the server SHALL place the workspace by the first placement rule that matches its name.

#### Scenario: Rule matches the workspace name
- **WHEN** no project is set and a rule matches the name
- **THEN** the workspace exists in the collection of that rule

### Requirement: Default at namespace create

When an administrator makes a namespace without the opt-out, the server SHALL store a default collection and a catch-all placement rule in the create transaction.

#### Scenario: Namespace gets the default
- **WHEN** an administrator makes a namespace without the opt-out
- **THEN** the namespace holds a collection named default, a catch-all rule that names its ID, and an audit entry for each

#### Scenario: Create fails midway
- **WHEN** the store of the rule fails
- **THEN** no namespace, collection or rule exists

### Requirement: Opt-out of the default

Where the namespace create request sets the opt-out, the server SHALL store no default collection and no catch-all rule.

#### Scenario: Namespace with the opt-out
- **WHEN** an administrator makes a namespace with the opt-out
- **THEN** the namespace holds no collection and no rule

#### Scenario: Administrator deletes the catch-all
- **WHEN** an administrator deletes the catch-all rule
- **THEN** a new stack that no rule matches is refused

### Requirement: Refuse stack with no match

If no placement rule matches a new Pulumi stack, then the server SHALL refuse the create with 422 and name the placement rule list.

#### Scenario: No rule matches
- **WHEN** the rule list is empty or no rule matches
- **THEN** the server answers 422, names the namespace and the stack, and no stack exists

### Requirement: Refuse workspace with no collection

If no project and no rule gives an existing collection for a new workspace, then the server SHALL refuse the create with 422 and name the collection list.

#### Scenario: Project missing or unknown
- **WHEN** the project is missing or unknown and no rule matches
- **THEN** the server answers 422, says to set project, to make the collection or to use the API, and no workspace exists

### Requirement: No implicit collection

If a CLI create finds no collection, then the server SHALL make neither a collection nor a unit.

#### Scenario: Refusal makes nothing
- **WHEN** a CLI create finds no collection
- **THEN** no collection and no unit exist

### Requirement: Grant check on the chosen collection

If the caller holds no grant to make a unit in the chosen collection, then the server SHALL refuse the create with 403.

#### Scenario: Caller has no grant
- **WHEN** the chosen collection exists and the caller has no grant there
- **THEN** the server answers 403, names the collection and the rule or project, and no unit exists

### Requirement: Order of checks

If the caller holds no right to make a unit in the namespace, then the server SHALL answer 403 before it tells whether a collection exists.

#### Scenario: No right and no match
- **WHEN** such a caller creates a stack that no rule matches
- **THEN** the server answers 403 only

#### Scenario: No read of the collection
- **WHEN** a caller can make units in the collection but cannot read it
- **THEN** the create succeeds and the answer shows only the unit

### Requirement: Record of a refusal

When the server refuses a CLI create for placement, the server SHALL write an audit entry with the unit and the reason.

#### Scenario: Refusal is audited
- **WHEN** a create is refused
- **THEN** the audit log holds an entry with the namespace, the unit and the reason

#### Scenario: Audit write fails
- **WHEN** the refusal audit write fails
- **THEN** the server still answers 422 and logs the failure

### Requirement: Record of a placement

When the server places a CLI-made unit, the server SHALL write the audit entry in the transaction that stores the unit.

#### Scenario: Placement is audited
- **WHEN** a rule places a stack
- **THEN** the audit log holds an entry with the unit, the collection and the rule

#### Scenario: Audit write fails
- **WHEN** the audit write fails
- **THEN** the transaction rolls back and no unit exists

### Requirement: Record of a rule change

When an administrator saves, reorders or deletes a placement rule, the server SHALL write an audit entry with the rule and the actor in the same transaction.

#### Scenario: Reorder is audited
- **WHEN** an administrator moves a rule to another position
- **THEN** the audit log holds an entry with the rule and the actor

#### Scenario: Update is audited
- **WHEN** an administrator updates a rule with PUT
- **THEN** the audit log holds an entry with the rule and the actor

### Requirement: Rule validation at save

If a placement rule names a collection that does not exist in its namespace, then the server SHALL refuse to save the rule.

#### Scenario: Missing collection
- **WHEN** an administrator saves a rule with an unknown collection ID
- **THEN** the server answers 422 and stores nothing

#### Scenario: Collection of another namespace
- **WHEN** the ID names a collection of another namespace
- **THEN** the server answers 422 and stores nothing

### Requirement: Pattern limits

If a pattern of a placement rule is longer than 128 bytes, holds a character that is not printable ASCII, or holds more than 4 asterisks, then the server SHALL refuse to save the rule.

#### Scenario: Hostile pattern
- **WHEN** a pattern has 129 bytes, a control character or 5 asterisks
- **THEN** the server answers 422 and stores nothing

### Requirement: Rule count limit

If a namespace holds 100 placement rules, then the server SHALL refuse to save one more rule.

#### Scenario: At the limit
- **WHEN** an administrator saves a rule in a namespace with 100 rules
- **THEN** the server answers 422 and stores nothing

#### Scenario: Below the limit
- **WHEN** an administrator saves the 100th rule
- **THEN** the rule is stored

### Requirement: Rule list order

When a caller lists the placement rules, the server SHALL return the rules in match order with the collection ID of each rule.

#### Scenario: List shows order
- **WHEN** an administrator moves a rule and lists the rules
- **THEN** the list shows the new order and the collection IDs

#### Scenario: Collection not readable
- **WHEN** the caller cannot read a collection that a rule names
- **THEN** the list still shows the rule with its collection ID

### Requirement: Collection in use

If a placement rule or a unit names a collection, then the server SHALL refuse to delete that collection.

#### Scenario: Delete of a named collection
- **WHEN** an administrator deletes a collection that a rule names
- **THEN** the server refuses and the collection stays

### Requirement: Same-name creates

If two CLI creates of one unit name run at the same time, then the server SHALL store one unit and answer the other create with a conflict error.

#### Scenario: Concurrent Pulumi create
- **WHEN** two processes make the same stack
- **THEN** one stack exists and the other gets a conflict

#### Scenario: Pulumi and TFE race
- **WHEN** a stack create and a workspace create use the same unit name
- **THEN** one unit exists and the other gets a conflict

### Requirement: Reorder with a create

When a rule reorder commits while a create runs, the server SHALL place the unit by the order that the create read in its transaction.

#### Scenario: Reorder races a create
- **WHEN** a create reads the rules before a reorder commits
- **THEN** the old order places the unit and the audit entry names the rule

### Requirement: Existing units stay

When an administrator changes or deletes a placement rule, the server SHALL keep the collection of each existing unit.

#### Scenario: Rule change does not move
- **WHEN** an administrator changes the rule that placed a stack
- **THEN** the stack stays in its collection
