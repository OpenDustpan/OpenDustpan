## ADDED Requirements

### Requirement: Refused last break-glass change

If a disable, delete or sweep targets the last enabled break-glass account, then the server SHALL write one critical notice to each other enabled site administrator.

#### Scenario: Delete of the last account
- **WHEN** a site administrator deletes the last enabled break-glass account
- **THEN** the server answers 409 principal.last-break-glass and writes one audit entry and one notice for each other enabled site administrator

#### Scenario: Sweep of the last account
- **WHEN** the inactivity sweep reaches the last enabled break-glass account
- **THEN** the account stays enabled and one audit entry and one notice exist

#### Scenario: Refused by the two-working-paths floor
- **WHEN** a change refused by the floor targets a break-glass account
- **THEN** the server writes one audit entry and one notice

### Requirement: Changed break-glass account

When a break-glass account is disabled or deleted, the server SHALL write one critical notice to each other enabled site administrator in the same transaction as the change.

#### Scenario: Disable with a second account
- **WHEN** a site administrator disables a break-glass account while another stays enabled
- **THEN** the account is disabled and one notice exists for each other enabled site administrator, and none for the actor

#### Scenario: Disable of a disabled account
- **WHEN** a site administrator disables a break-glass account that is already disabled
- **THEN** the server writes no notice

### Requirement: No other administrator

If no other enabled site administrator exists, then the server SHALL write the audit entry and no notice.

#### Scenario: Only the actor is enabled
- **WHEN** the actor is the only enabled site administrator and disables a break-glass account
- **THEN** one audit entry exists and no notice exists

### Requirement: Refusal notice limit

While a refusal notice for the same actor and target is younger than 10 minutes, the server SHALL write no new refusal notice.

#### Scenario: Repeated refusal
- **WHEN** an administrator repeats a refused delete 50 times in 1 minute
- **THEN** 50 audit entries and one notice exist
