## ADDED Requirements

### Requirement: Group trust flag

When a caller with a fresh authentication and a reach that covers the reach of the group sets trust, the server SHALL mark the group as trusted with setter and time.

#### Scenario: Set trust
- **WHEN** a covered caller with a fresh step sets trust on a group
- **THEN** the group is trusted with setter and time and an audit entry is written

#### Scenario: Two changes
- **WHEN** two callers change trust on one group at once
- **THEN** both changes are audited and the last committed change is the stored value

#### Scenario: Write group
- **WHEN** a covered caller with a fresh step sets trust on a group with installation-wide write access
- **THEN** the group is trusted, because trust overrides protection, and an audit entry is written

#### Scenario: Sync during change
- **WHEN** a membership sync runs while trust changes
- **THEN** the sync uses the value committed when it started

#### Scenario: Recreated group
- **WHEN** a trusted group is removed and made again
- **THEN** the new group has a new ID and is not trusted

### Requirement: Trusted group membership

While a group is trusted, the server SHALL accept an add of a member from the identity provider, including to a group whose reach holds a protected action.

#### Scenario: Protected add
- **WHEN** the identity provider adds a member to a trusted group with a protected action
- **THEN** the membership is added

#### Scenario: Clear trust
- **WHEN** a covered caller clears trust and the identity provider adds a member
- **THEN** the server refuses the add and writes an audit entry

#### Scenario: Closed group
- **WHEN** trust is set on a group that a person closed with the switch
- **THEN** the group stays closed and the add is refused

### Requirement: Refusal without a fresh step

If the caller has no fresh authentication, then the server SHALL refuse the trust change with 403.

#### Scenario: Stale step
- **WHEN** a caller with a stale step sets trust
- **THEN** the server answers 403 mfa.fresh-step-required

#### Scenario: No factor
- **WHEN** a person of the OAuth or trusted-header method with no enrolled factor sets trust
- **THEN** the server answers 403 mfa.fresh-step-required and names the factor path

#### Scenario: Agent token
- **WHEN** an agent token sets trust
- **THEN** the server answers 403 mfa.fresh-step-required

#### Scenario: IdP-started SAML
- **WHEN** a person who signed in by SAML started at the identity provider sets trust
- **THEN** the server answers 403 mfa.fresh-step-required

### Requirement: Uncovered reach

If the reach of the caller does not cover the reach of the group, then the server SHALL refuse the trust change.

#### Scenario: Uncovered
- **WHEN** an uncovered caller sets trust
- **THEN** the server answers 403 and writes an audit entry

#### Scenario: No action
- **WHEN** a caller without server:manage-sign-in sets trust
- **THEN** the server answers 403

### Requirement: Input check

If the group ID is unknown or the input is not valid, then the server SHALL refuse the trust change.

#### Scenario: Unknown group
- **WHEN** a caller sets trust on an unknown group ID
- **THEN** the server answers 404

#### Scenario: Bad input
- **WHEN** a caller sends a body that is not valid
- **THEN** the server answers 400

### Requirement: Break-glass member

While a group is trusted, the server SHALL keep each break-glass member of the group unchanged.

#### Scenario: Trusted group with break-glass
- **WHEN** the identity provider removes members from a trusted group that holds a break-glass account
- **THEN** the break-glass membership stays
