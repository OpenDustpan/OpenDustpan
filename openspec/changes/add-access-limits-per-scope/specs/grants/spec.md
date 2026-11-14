## ADDED Requirements

### Requirement: expired-grant-denies

When a grant holds expiresAt in the past, the server SHALL give no access from that grant.

#### Scenario: past expiry
- **WHEN** a grant holds expiresAt in the past
- **THEN** the check treats the grant as absent and the access review marks it expired

### Requirement: save-past-expiry

When a caller saves a grant with expiresAt in the past, the server SHALL store a grant that gives no access.

#### Scenario: past time at save
- **WHEN** a caller saves a grant with expiresAt in the past
- **THEN** the stored grant gives no access and the review marks it expired

### Requirement: check-clock

When the server time reaches expiresAt, the server SHALL treat the grant as expired.

#### Scenario: expiry boundary
- **WHEN** the server time reaches expiresAt
- **THEN** the grant gives access before the time and no access at and after the time

### Requirement: check-independent-of-sweep

When a grant passes expiresAt, the server SHALL deny it at check time even before the sweep marks it.

#### Scenario: check before sweep
- **WHEN** a caller uses a grant past expiresAt before the sweep runs
- **THEN** the call gets 403 even though the sweep has not marked the row

### Requirement: grant-expiry-sweep

When a grant passes expiresAt, the server SHALL mark the grant as expired.

#### Scenario: sweep marks past grants
- **WHEN** the sweep runs after a grant passes expiresAt
- **THEN** the review shows each past grant as expired with its expiresAt value

### Requirement: grant-expiry-review

When a caller exports the access review, the server SHALL mark each grant past expiresAt as expired.

#### Scenario: export with expired row
- **WHEN** a caller exports the access review
- **THEN** the row shows expired with its expiresAt value

### Requirement: history-end

When a caller exports the grant history, the server SHALL show the expiry time as the end time of an expired grant.

#### Scenario: history export with expired grant
- **WHEN** a caller exports the grant history across the expiry time
- **THEN** the history row shows the expiry time as the end time of the grant
