## ADDED Requirements

### Requirement: Ownership rule and path-set validation

When a namespace administrator saves an ownership rule or a path set, the server SHALL check the ID, the path pattern, the condition size and the scope limits.

#### Scenario: Save rule with bad pattern
- **WHEN** A namespace administrator saves an ownership rule with a bad path pattern or a bad condition
- **THEN** The save fails with 422 gate.invalid and names the bad field.

#### Scenario: Save above limits
- **WHEN** A scope already holds 256 ownership rules or 64 path sets or the condition is above 4 KiB
- **THEN** The save fails with 422 gate.limit.

### Requirement: Ownership audit entry

When a namespace administrator creates, changes or deletes an ownership rule or a path set, the server SHALL write an audit entry of kind change with the old and the new version.

#### Scenario: Create ownership rule
- **WHEN** A namespace administrator creates, changes or deletes an ownership rule or a path set
- **THEN** The audit log holds one entry of kind change with the old and the new version.

### Requirement: Path match adds owner requirement

When a plan touches a path set of an ownership rule in enforce mode, the server SHALL add exactly one approval requirement for the owners of each matched rule.

#### Scenario: Plan touches owned path set
- **WHEN** A plan in enforce mode touches a path set of an ownership rule
- **THEN** The decision record holds one requirement for the owners of each matched rule.

#### Scenario: Two rules match one plan
- **WHEN** A plan touches path sets of two enforce ownership rules
- **THEN** The decision record holds two requirements, one for each rule.

### Requirement: Evaluate mode records only

When a plan touches a path set of an ownership rule in evaluate mode, the server SHALL record the match with no approval requirement.

#### Scenario: Plan touches evaluate rule
- **WHEN** A plan touches a path set of an ownership rule in evaluate mode
- **THEN** The decision record names the rule and the match and holds no new requirement.

### Requirement: Conditional auto-apply by ownership unless

When a plan meets the unless condition of its matched ownership rule over the facts of change.planned, the server SHALL mark the decision record autoApply true for that ownership requirement.

#### Scenario: Schedule plan inside unless
- **WHEN** A schedule-started plan has no changes outside the unless condition of its matched ownership rule
- **THEN** The decision record shows autoApply true with the matched rule and the true unless result, and the apply starts with no approval for that requirement.

### Requirement: Warn stops auto-apply

When a plan that meets an unless condition holds a warn, the server SHALL mark autoApply false.

#### Scenario: Warn on auto-apply candidate
- **WHEN** A plan that meets the unless condition holds a warn
- **THEN** The decision record shows autoApply false and the apply waits for approval.

### Requirement: Unless error fails closed

When the unless evaluation over change.planned facts fails, the server SHALL mark autoApply false.

#### Scenario: Unknown fact in unless
- **WHEN** The unless evaluation reads an unknown fact or the Cedar evaluation fails
- **THEN** The decision record shows autoApply false and the unless result error, and the apply waits for approval.

### Requirement: Author-only owners still wait

When a matched ownership rule leaves no eligible approver other than the author, the server SHALL keep the requirement in approval wait.

#### Scenario: Author is sole eligible approver
- **WHEN** A matched rule leaves no eligible approver other than the author and authorMayApprove is false
- **THEN** The requirement stays in approval wait and names its eligible approvers.

### Requirement: Record shows rule and unless

When the server evaluates an ownership rule at change.planned, the server SHALL write the rule ID, the matched path set and the unless result in the decision record.

#### Scenario: Read decision record
- **WHEN** An ownership rule was evaluated at change.planned
- **THEN** The record names the rule ID, the matched path set and the unless result.
