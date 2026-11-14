## ADDED Requirements

### Requirement: limit-passing-proof-denials

When a source has 30 denials after proof verification in 60 seconds, the server SHALL refuse the next exchange of that source with runner.limit.

#### Scenario: flood
- **WHEN** one source gets 31 denials in 60 seconds
- **THEN** the 31st answer is runner.limit with no credential and no per-denial audit entry

### Requirement: audit-bad-proof

When the proof of an exchange fails verification, the server SHALL write one audit entry with a reason class and no proof bytes.

#### Scenario: bad proof
- **WHEN** the proof fails verification
- **THEN** one audit entry holds the reason class and no proof bytes

### Requirement: ignore-malformed

When the proof of an exchange is malformed at the boundary, the server SHALL answer runner.invalid with no audit entry and no count.

#### Scenario: malformed
- **WHEN** the proof is malformed
- **THEN** the answer is runner.invalid with no audit entry and no count

### Requirement: valid-success

When the proof of an exchange is valid and allowed, the server SHALL issue the credential with no change to the denial count.

#### Scenario: valid after flood
- **WHEN** a valid proof comes from a source over its limit
- **THEN** the credential is issued and the count is unchanged

### Requirement: fail-closed

If the limiter check or the audit write fails, then the server SHALL deny the exchange with no credential.

#### Scenario: write fails
- **WHEN** the audit write fails
- **THEN** the server denies with no credential

### Requirement: suppressed-summary

Where denials are suppressed over the limit, the server SHALL write one summary audit entry for each source in each 60 seconds.

#### Scenario: summary
- **WHEN** denials are suppressed in a window
- **THEN** one summary entry holds the count and the NEW counter runner_exchange_suppressed_denials_total counts them
