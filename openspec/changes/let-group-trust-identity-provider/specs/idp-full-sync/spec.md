## ADDED Requirements

### Requirement: Full sync setting

When a site administrator with a fresh authentication turns full sync on, the server SHALL treat every group as trusted from the identity provider.

#### Scenario: Turn on
- **WHEN** full sync is turned on
- **THEN** the identity provider can add members to every group and an audit entry is written

#### Scenario: Turn off
- **WHEN** full sync is turned off
- **THEN** groups that are not trusted refuse the add again, removals that were final stay final, and an audit entry is written

### Requirement: Full sync needs a fresh step

If the caller has no fresh authentication, then the server SHALL refuse the full sync change with 403.

#### Scenario: Stale step
- **WHEN** a caller with a stale step changes full sync
- **THEN** the server answers 403 mfa.fresh-step-required

#### Scenario: No factor
- **WHEN** a person of the OAuth or trusted-header method with no enrolled factor changes full sync
- **THEN** the server answers 403 mfa.fresh-step-required and names the factor path

### Requirement: Break-glass exempt

While full sync is on, the server SHALL keep each break-glass account unchanged.

#### Scenario: Sync over break-glass
- **WHEN** full sync processes a break-glass account
- **THEN** the account stays unchanged
