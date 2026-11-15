## ADDED Requirements

### Requirement: expired-grant-denies

When a grant holds expiresAt in the past, the server SHALL give no access from that grant.

#### Scenario: past expiry
- **WHEN** a grant holds expiresAt in the past
- **THEN** the check treats the grant as absent and the access review marks it expired

### Requirement: check-independent-of-sweep

When a grant passes expiresAt, the server SHALL deny it at check time even before the sweep marks it.

#### Scenario: check before sweep
- **WHEN** a caller uses a grant past expiresAt before the sweep runs
- **THEN** the call gets 403

### Requirement: grant-expiry-sweep

When a grant passes expiresAt, the server SHALL mark the grant as expired.

#### Scenario: sweep marks past grants
- **WHEN** the leased sweep runs after a grant passes expiresAt
- **THEN** the review shows the grant as expired with its expiresAt value

### Requirement: sweep-lease-audit

While the grant-expiry sweep runs, the server SHALL hold one store lease and write one audit entry for the run.

#### Scenario: one sweep run
- **WHEN** two sweep workers start at the same time
- **THEN** only one worker marks rows and the audit log holds one entry

### Requirement: history-end

When a caller exports the grant history, the server SHALL show the expiry time as the end time of an expired grant.

#### Scenario: history export
- **WHEN** a caller exports history across the expiry time
- **THEN** the row shows the expiry time as the end time

### Requirement: group-grant-expiry

When a grant to a group holds expiresAt in the past, the server SHALL give no access from that grant.

#### Scenario: expired group grant
- **WHEN** a group grant holds expiresAt in the past
- **THEN** the member gets no access from that grant
