## ADDED Requirements

### Requirement: Group trust flag

When a caller with a fresh authentication and a reach that covers the reach of the group sets trust, the server SHALL mark the group as trusted with setter and time.

#### Scenario: Set trust
- **WHEN** a covered caller with a fresh step sets trust on a group
- **THEN** the group is trusted with setter and time and an audit entry is written

#### Scenario: Two changes
- **WHEN** two callers change trust on one group at once
- **THEN** both changes are audited and the last committed change is the stored value

### Requirement: Refusal without a fresh step

If the caller has no fresh authentication, then the server SHALL refuse the trust change with 403.

#### Scenario: Stale step
- **WHEN** a caller with a stale step sets trust
- **THEN** the server answers 403 mfa.fresh-step-required

#### Scenario: No factor
- **WHEN** a person with no enrolled factor sets trust
- **THEN** the server answers 403 mfa.fresh-step-required and names the factor path

### Requirement: Uncovered reach

If the reach of the caller does not cover the reach of the group, then the server SHALL refuse the trust change.

#### Scenario: Uncovered
- **WHEN** an uncovered caller sets trust
- **THEN** the server answers 403 and writes an audit entry
