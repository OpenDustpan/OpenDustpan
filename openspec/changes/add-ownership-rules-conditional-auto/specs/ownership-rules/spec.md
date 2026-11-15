## ADDED Requirements

### Requirement: Path match adds owner requirement

When a plan touches a path set of an ownership rule in enforce mode, the server SHALL add one approval requirement for the owners of each matched rule.

#### Scenario: Two rules match
- **WHEN** a plan touches path sets of two enforce rules
- **THEN** the record holds two requirements

### Requirement: Evaluate mode records only

When a plan touches a path set of an ownership rule in evaluate mode, the server SHALL record the match with no requirement.

#### Scenario: Evaluate rule
- **WHEN** a plan touches an evaluate rule
- **THEN** the record names the match and holds no new requirement

### Requirement: Conditional auto-apply

When a plan meets the unless condition of its matched ownership rule, the server SHALL mark autoApply true for that requirement.

#### Scenario: Schedule plan
- **WHEN** a schedule-started plan meets the unless condition
- **THEN** the record shows autoApply true and the apply starts with no approval for that requirement

### Requirement: Warn stops auto-apply

When a plan that meets an unless condition holds a warn, the server SHALL mark autoApply false.

#### Scenario: Warn present
- **WHEN** the plan holds a warn
- **THEN** autoApply is false and the apply waits for approval

### Requirement: Unless error fails closed

If the unless evaluation fails, then the server SHALL mark autoApply false.

#### Scenario: Unknown fact
- **WHEN** the unless reads an unknown fact
- **THEN** autoApply is false and the record shows the error

### Requirement: Operation source fact

When the server builds the facts of change.planned, the server SHALL include operationSource with one of push, schedule, follows, merge or api.

#### Scenario: Missing source
- **WHEN** operationSource is missing
- **THEN** autoApply is false

### Requirement: Excuse scope

When the unless of an ownership rule is true, the server SHALL excuse only that ownership requirement.

#### Scenario: Gate still waits
- **WHEN** a gate requirement also applies
- **THEN** the change waits for the gate requirement

### Requirement: Apply recheck only adds

When the server evaluates change.apply after change.planned, the server SHALL add each newly matched ownership requirement and remove none.

#### Scenario: Rule saved after plan
- **WHEN** a matching rule is saved after the plan
- **THEN** the apply waits for the owner approval

### Requirement: Record shows rule and unless

When the server evaluates an ownership rule at change.planned, the server SHALL write the rule ID, the matched path set and the unless result in the decision record.

#### Scenario: Read record
- **WHEN** a rule was evaluated
- **THEN** the record names the rule, the path set and the unless result
