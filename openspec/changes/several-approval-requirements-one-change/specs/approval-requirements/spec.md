## ADDED Requirements

### Requirement: Distinct approvers across requirements

When a change holds more than one approval requirement, the server SHALL allow the apply only with distinct approvers that satisfy each requirement.

#### Scenario: Two requirements need distinct people
- **WHEN** a namespace rule needs 2 of group platform and a collection rule needs 1 of any person
- **THEN** the apply stays in awaiting-approval until both requirements hold with no shared approver

#### Scenario: Greedy order still finds a valid set
- **WHEN** one approver is eligible for both requirements and a second approver is eligible for only one requirement
- **THEN** the server accepts the set and allows the apply

### Requirement: Apply refusal names missing approvers

When an apply arrives with an unmet approval requirement, the server SHALL refuse with 403 that names each unmet requirement and the count of missing distinct approvers.

#### Scenario: Apply with one requirement unmet
- **WHEN** an apply arrives with an unmet approval requirement
- **THEN** the server refuses with 403 that names each unmet requirement and the count of missing distinct approvers

### Requirement: One principal counts once

When one principal submits more than one approval for one change, the server SHALL count that principal once.

#### Scenario: Duplicate concurrent approvals
- **WHEN** one principal submits two approvals for one change at the same time
- **THEN** the server counts that principal once and still waits for the other distinct approver

### Requirement: Concurrent applies allow one winner

When 2 applies for one change start at the same time, the server SHALL allow exactly 1 apply through RecheckAtApply.

#### Scenario: RecheckAtApply race
- **WHEN** 2 applies for one change start within the same 100 milliseconds with RecheckAtApply in the apply transaction
- **THEN** exactly one of the 2 applies proceeds and the other gets 409 with no double apply

### Requirement: Users and unit owners as approvers

When an approval requirement names users or unit owners, the server SHALL count approvals only from principals whose stable ID is eligible at apply time.

#### Scenario: User and owner approvals count
- **WHEN** a requirement names users or unit owners as eligible approvers
- **THEN** the requirement counts each approval once

#### Scenario: Eligibility lost before apply
- **WHEN** an approver leaves the named group, stops owning the unit, or returns under a remade principal with a distinct stable ID
- **THEN** the earlier approval no longer satisfies the requirement

### Requirement: Self approval excluded

When the principal who requested the change or started the apply submits an approval, the server SHALL exclude that approval from every requirement.

#### Scenario: Requester approves own change
- **WHEN** the principal who requested the change or started the apply submits an approval
- **THEN** that approval does not satisfy any requirement and the apply still waits

### Requirement: Approvals bind to plan revision

When a new plan revision arrives for a change, the server SHALL invalidate approvals recorded for an earlier revision.

#### Scenario: Plan changes after approval
- **WHEN** a new plan revision arrives after approvals were recorded
- **THEN** the earlier approvals no longer satisfy any requirement

#### Scenario: Withdrawal races apply
- **WHEN** a withdrawal lands while the apply transaction runs
- **THEN** RecheckAtApply sees the withdrawal and refuses the apply

### Requirement: Withdrawn and late approvals

When an approval is withdrawn before the apply, the server SHALL no longer count that approval.

#### Scenario: Approval withdrawn before apply
- **WHEN** an approver withdraws an approval before the apply
- **THEN** that approval no longer satisfies any requirement

#### Scenario: Approval arrives after apply start
- **WHEN** an approval arrives after the apply started
- **THEN** the server refuses the approval

### Requirement: Fresh authentication window

When an approval requirement sets freshAuthSeconds, the server SHALL refuse an approval whose sign-in age exceeds that window.

#### Scenario: Old sign-in refused
- **WHEN** an approval arrives after freshAuthSeconds from the sign-in time
- **THEN** the server refuses the approval with the window value

#### Scenario: Revoked session refused
- **WHEN** the actor session was revoked after approval and before apply
- **THEN** the server refuses the approval even inside the window

#### Scenario: Window expires before apply
- **WHEN** an approval was fresh at approval time but its sign-in age exceeds freshAuthSeconds at apply time
- **THEN** the server refuses the apply and names the expired approval

### Requirement: Per requirement fresh window

When one change holds requirements with different freshAuthSeconds values, the server SHALL enforce the window of the assigned requirement for each approval.

#### Scenario: Different windows across requirements
- **WHEN** one change holds requirements with different freshAuthSeconds values
- **THEN** the approval counts only toward requirements whose window it meets

### Requirement: Unless exemption

When the unless body of an approval requirement matches, the server SHALL mark that requirement exempt with no approvals needed.

#### Scenario: Exempt change skips that approval
- **WHEN** the unless body matches the change facts
- **THEN** the requirement shows exempt with the matched reason and needs no approvals while other unmet requirements still block the apply

### Requirement: All requirements exempt

When the unless body of every requirement matches, the server SHALL allow the apply with no approvals.

#### Scenario: Every requirement exempt
- **WHEN** the unless body of every requirement matches
- **THEN** the server allows the apply with no approvals and records each exemption

### Requirement: Unless error is not exempt

If the unless body of an approval requirement errors, then the server SHALL treat that requirement as not exempt.

#### Scenario: Unless body errors
- **WHEN** the gate engine cannot evaluate the unless body
- **THEN** the requirement stays unmet and the apply still waits

#### Scenario: Unless body times out or is oversize
- **WHEN** the unless evaluation exceeds 5 seconds or 64 KB
- **THEN** the requirement stays unmet and the apply still waits

### Requirement: Unless exemption audit record

When an unless body exempts an approval requirement, the server SHALL write an audit entry that names the requirement, its setter, the matched reason, and the time.

#### Scenario: Exemption is recorded
- **WHEN** an unless body exempts a requirement
- **THEN** the audit log holds the requirement, the setter, the matched reason, and the time

### Requirement: Requirement cap

When a setter stores more than 10 approval requirements for one change scope, the server SHALL refuse the set.

#### Scenario: Eleventh requirement refused
- **WHEN** a setter stores an 11th approval requirement for one change scope
- **THEN** the server refuses the set with 400 and keeps the stored 10

### Requirement: Empty eligible set stays unmet

When a requirement names an empty eligible approver set, the server SHALL keep that requirement unmet.

#### Scenario: No eligible approver exists
- **WHEN** a requirement names an empty eligible approver set
- **THEN** the server refuses the apply with 403 that names the requirement

### Requirement: Gate read shows waiting state

When a developer reads the gate list for a change that waits for approvals, the server SHALL show each unmet requirement with the missing count and the window value.

#### Scenario: Developer reads gates before apply
- **WHEN** a developer reads the gate list for a change that waits for approvals
- **THEN** the read names each unmet requirement, the missing distinct-approver count, and the freshAuthSeconds value
