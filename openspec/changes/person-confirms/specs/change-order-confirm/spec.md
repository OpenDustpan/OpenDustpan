## ADDED Requirements

### Requirement: agent-cannot-confirm

When an agent requests the confirmation of a draft, the server SHALL refuse the request and SHALL write a denial entry that names the agent and the delegation.

#### Scenario: agent-confirm-through-a-delegation
- **WHEN** an agent principal requests a confirm under a delegation
- **THEN** the server refuses it, changes nothing and writes one denial entry

#### Scenario: agent-confirm-with-its-own-token
- **WHEN** an agent principal requests a confirm with its own token
- **THEN** the server refuses it and writes one denial entry

### Requirement: delegator-is-the-author

When an agent drafted a change for its delegator, the server SHALL count the delegator as the author for the approval rules, and a later end of the delegation SHALL NOT change it.

#### Scenario: two-person-rule
- **WHEN** the namespace sets `authorMayApprove` false and the delegator confirms and then approves
- **THEN** the second approval is refused with `access.denied` until a different eligible person approves

#### Scenario: one-person-installation
- **WHEN** the namespace sets `authorMayApprove` true and the delegator confirms and then approves
- **THEN** the approval is accepted

#### Scenario: ended-delegation-keeps-the-author
- **WHEN** the delegation ended after the draft
- **THEN** the delegator still counts as the author

### Requirement: confirmation-is-audited-before-apply

When a person confirms a draft, the server SHALL write the confirmation entry in the same transaction as the confirm, before any apply entry of that change.

#### Scenario: confirm-before-apply
- **WHEN** a person confirms a draft and an apply runs later
- **THEN** the audit log holds the confirmation entry before the first apply entry

### Requirement: fresh-step-follows-the-setting

When the installation setting `freshAuthEnabled` is on, the server SHALL refuse a confirm that has no fresh step, and an access token SHALL NOT give one. When the setting is off, the server SHALL ask for no step.

#### Scenario: access-token-with-the-setting-on
- **WHEN** a person confirms with an access token while `freshAuthEnabled` is on
- **THEN** the server answers 403 with `mfa.fresh-step-required`

#### Scenario: access-token-with-the-setting-off
- **WHEN** a person who holds the grant confirms with an access token while `freshAuthEnabled` is off
- **THEN** the server asks for no fresh step
