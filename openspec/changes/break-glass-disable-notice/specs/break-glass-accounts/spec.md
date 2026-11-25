## ADDED Requirements

### Requirement: Disable notice

The server SHALL append one notification of type `break-glass.disabled`, with severity warning, to the outbox of the server scope for the administrators of the installation after the disable of a break-glass account is stored. The notification SHALL name the stable ID of the account, the actor and the number of enabled break-glass accounts that remain.

#### Scenario: Disable of one of two enabled accounts

- **WHEN** an administrator disables one of two enabled break-glass accounts
- **THEN** one notification of type `break-glass.disabled` exists for the administrators
- **AND** its attribute `enabled.remaining` is 1

#### Scenario: Refused disable

- **WHEN** the disable is refused because it would leave no enabled break-glass account
- **THEN** no notification is appended

#### Scenario: Account that is not a break-glass account

- **WHEN** an administrator disables a principal that is not a break-glass account
- **THEN** no notification of type `break-glass.disabled` is appended

### Requirement: Disable notice audited

The server SHALL write one audit entry with the action `principal:break-glass-disable-notice` for each notice, and one entry of outcome failure when the notice cannot be stored.

#### Scenario: Notice stored

- **WHEN** the notice is stored
- **THEN** one audit entry of outcome success names the account and the actor

#### Scenario: Outbox failure

- **WHEN** the outbox refuses the notice
- **THEN** the disable stays stored
- **AND** the caller gets an error
- **AND** one audit entry of outcome failure exists
