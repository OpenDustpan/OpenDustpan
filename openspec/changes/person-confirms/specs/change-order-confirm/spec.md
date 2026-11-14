## ADDED Requirements

### Requirement: Agent cannot confirm

When an agent requests confirmation of a change order, the server SHALL refuse the request.

#### Scenario: Agent confirm through delegation refused
- **WHEN** an agent principal requests confirmation with the delegation credential
- **THEN** the server answers 403 with the NEW error code changeorder.agent-confirm-refused and writes an audit entry that names the agent and the delegation.

#### Scenario: Agent confirm with own token refused
- **WHEN** an agent principal requests approval with its own token and no delegation
- **THEN** the server answers 403 with the NEW error code changeorder.agent-confirm-refused.

### Requirement: Confirm route and state

Where the NEW confirm route is called, the server SHALL authorize the NEW action changeorder:confirm on the namespace object.

#### Scenario: Person confirms and meets rule
- **WHEN** a person with the grant calls the NEW confirm route with the current revision ID
- **THEN** the order moves from in-review to approved for the named revision.

#### Scenario: Person confirms and needs second approver
- **WHEN** a person calls the NEW confirm route while the approval rule is not yet met
- **THEN** the order records the confirmation and stays in-review until a different eligible person approves.

### Requirement: Delegator is author

When a change order was proposed by an agent for its delegator, the server SHALL count the delegator stored at proposal time as the author for approval rules.

#### Scenario: Two-person rule blocks delegator as second approver
- **WHEN** the namespace sets authorMayApprove false and the delegator confirms then approves
- **THEN** the second approval is refused with approval.not-eligible until a different eligible person approves.

#### Scenario: Revoked delegation still leaves delegator as author
- **WHEN** the delegation expired or was revoked after the proposal stored the delegator subject
- **THEN** a person with the grant confirms the order and the delegator still counts as author.

#### Scenario: One-person installation allows delegator alone
- **WHEN** the namespace sets authorMayApprove true and the delegator confirms then approves
- **THEN** the order moves to approved and an apply job may start.

### Requirement: Confirm binds revision atomically

When a person confirms a change order, the server SHALL bind the confirmation to the named plan revision ID.

#### Scenario: New revision needs new confirm
- **WHEN** a new plan revision arrives after a person confirmation
- **THEN** the order returns to in-review and the old confirmation does not authorize apply or destroy of the new revision.

#### Scenario: Revision race refused
- **WHEN** a new plan revision commits while a confirm request for the old revision is in flight
- **THEN** the late confirm is refused with 409 and the order stays on the new revision without approval.

#### Scenario: Concurrent confirms apply once
- **WHEN** two person confirms for the same revision arrive at once
- **THEN** one confirmation wins and the second reports the order already confirmed for that revision.

### Requirement: No apply before approval

While a change order is not approved, the server SHALL refuse each apply job and each destroy job for its revisions.

#### Scenario: Apply blocked while proposed
- **WHEN** an apply is requested while the change order is in state proposed
- **THEN** the apply request is refused with 409 and the NEW error code changeorder.not-ready and no apply step runs.

#### Scenario: Destroy blocked while not approved
- **WHEN** a destroy is requested while the change order is not approved
- **THEN** the destroy request is refused with 409 and the NEW error code changeorder.not-ready and no destroy step runs.

#### Scenario: Queued destroy cancelled by new revision
- **WHEN** a new plan revision arrives before a queued destroy job starts
- **THEN** the queued destroy does not start and the order needs a new person confirmation.

#### Scenario: Person confirms then apply runs
- **WHEN** a person confirms the revision and then apply is requested
- **THEN** the apply job starts and the audit log shows the person confirmation before the first apply step.

### Requirement: Audit shows no apply before confirm

When a change order moves from proposed to approved, the server SHALL write a person confirmation audit entry before any apply entry of the revision.

#### Scenario: Audit export has confirm before apply
- **WHEN** a person confirms the revision and then apply runs
- **THEN** the export holds the person confirmation entry before any apply entry for the revision and no apply entry before it.

### Requirement: Fresh step for confirm

When confirmation needs a fresh step, the server SHALL refuse a confirm request that carries only an access token.

#### Scenario: Access token without fresh step refused
- **WHEN** a person confirms with an access token while the namespace needs a fresh step
- **THEN** the server refuses with 403 and the error code mfa.fresh-step-required.

### Requirement: Grant loss re-check

When the grant of a confirmer ends before approval completes, the server SHALL re-evaluate the grant at the approval check.

#### Scenario: Grant removed between confirm and approval
- **WHEN** the grant of the confirmer is removed or the person is disabled after confirmation
- **THEN** the later approval is refused and the order stays in-review.
