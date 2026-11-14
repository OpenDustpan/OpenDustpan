## ADDED Requirements

### Requirement: Claim entry

When the claimer applies EventClaim, the server SHALL write one audit entry for that claim.

#### Scenario: Claim writes entry
- **WHEN** the claimer applies EventClaim
- **THEN** the audit log holds one entry with the runner ID as subject, the job ID, and the namespace ID.

### Requirement: Lost claim writes none

When the claimer rejects a claim, the server SHALL write no audit entry for that claim.

#### Scenario: Lost race writes none
- **WHEN** a claim loses a race and the server rejects it
- **THEN** the server writes no audit entry for the rejected claim.

### Requirement: Cross-namespace claim writes none

When a runner token names a job outside its namespace, the server SHALL reject the call with no audit entry.

#### Scenario: Wrong namespace writes none
- **WHEN** a runner token of one namespace claims a job of another namespace
- **THEN** the server rejects the call with no state change and no audit entry.

### Requirement: Release entry

When the claimer applies EventRelease, the server SHALL write one audit entry for that release.

#### Scenario: Release writes entry
- **WHEN** the claimer applies EventRelease
- **THEN** the audit log holds one entry with the runner ID as subject, the job ID, and the namespace ID.

### Requirement: Phase failed entry

When the claimer applies EventPhaseFailed, the server SHALL write one audit entry for that failure.

#### Scenario: Phase failed writes entry
- **WHEN** the claimer applies EventPhaseFailed
- **THEN** the audit log holds one entry with the runner ID as subject, the job ID, and the namespace ID.

### Requirement: Finish entry

When the claimer applies a finish event, the server SHALL write one audit entry for that finish.

#### Scenario: Finish writes entry
- **WHEN** the claimer applies a finish event
- **THEN** the audit log holds one entry with the runner ID as subject, the job ID, and the namespace ID.

### Requirement: Server-driven entry

When the claimer applies a server-driven event, the server SHALL write one audit entry with the server actor and the reason.

#### Scenario: Server release writes entry
- **WHEN** the claimer applies a server-driven release, lease expiry, or restart retry
- **THEN** the audit log holds one entry with the server actor, one reason of lease-expiry, restart-retry, or server-release, the job ID, and the namespace ID.

### Requirement: Duplicate writes none

When the claimer receives a duplicate event, the server SHALL write no new audit entry for that retry.

#### Scenario: Retried event writes none
- **WHEN** a runner retries an event that the claimer already applied
- **THEN** the server returns the prior result with no new audit entry.

### Requirement: Audit write failure fails commit

When the audit write fails, the server SHALL fail the event commit with no state change.

#### Scenario: Failed audit write fails event
- **WHEN** the audit write fails during the claimer commit
- **THEN** the job state stays unchanged, no audit entry exists, and the runner receives an error to retry.
