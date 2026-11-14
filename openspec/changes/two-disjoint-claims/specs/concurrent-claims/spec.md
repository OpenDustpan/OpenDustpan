## ADDED Requirements

### Requirement: Register disjoint claim

When a caller with unit:claim-register on the workspace registers a non-replace claim disjoint from each accepted claim, the server SHALL store the claim with state accepted, start time and principal.

#### Scenario: Second disjoint claim accepted
- **WHEN** a runner job registers a non-replace claim that shares no address with each accepted claim
- **THEN** the server stores the second claim as accepted with start time and principal and keeps the first accepted

#### Scenario: Local wrapper claim accepted
- **WHEN** a local wrapper with the action registers a disjoint non-replace claim through the same route
- **THEN** the server stores the claim as accepted with a principal and no runner job ID

### Requirement: Refuse overlapping claim

When a caller registers a non-replace claim that shares an address with an accepted claim, the server SHALL answer 409 with the blocking claim ID and store no claim.

#### Scenario: Overlap refused
- **WHEN** a caller registers a claim that shares an address with an accepted claim
- **THEN** the server answers 409 with the blocking claim ID and stores no claim

### Requirement: Reject empty claim set

When a caller registers a claim with an empty resource set, the server SHALL answer 422 and store no claim.

#### Scenario: Empty set rejected
- **WHEN** a caller registers an empty set
- **THEN** the server answers 422 and stores no claim

### Requirement: Reject touched stale base

When a later state version touched an address of the claim after its base, the server SHALL answer 422 and store no claim.

#### Scenario: Touched base rejected
- **WHEN** a later version touched an address of the claim after its base
- **THEN** the server answers 422 with the touching version and stores no claim

#### Scenario: Untouched older base accepted
- **WHEN** no later version touched an address of a disjoint claim on an older base
- **THEN** the server stores the claim as accepted

### Requirement: Reject replace claim

When a caller registers a claim that holds a replace action, the server SHALL answer 422 and store no claim.

#### Scenario: Replace refused
- **WHEN** a claim holds a replace action
- **THEN** the server answers 422 and stores no claim

### Requirement: Authorize claim registration

When a caller without unit:claim-register on the workspace registers a claim, the server SHALL answer 403 and store no claim.

#### Scenario: Missing action denied
- **WHEN** a caller without the action registers a claim
- **THEN** the server answers 403 and stores no claim

### Requirement: Read claim list

When a caller with unit:read on the workspace lists claims, the server SHALL return each claim with its state, base, address set, principal, start time and end time.

#### Scenario: List shows both claims
- **WHEN** a caller lists claims after two disjoint registrations
- **THEN** the server lists both claims with state, base, address set, principal, start time and end time when set

### Requirement: Idempotent claim retry

When the same job sends the same base state version and the same normalized address set again, the server SHALL return the same claim ID without a second record.

#### Scenario: Same content retried
- **WHEN** the same job sends the same base and the same set again
- **THEN** the server answers success with the same claim ID and stores no second record

#### Scenario: Different set is a new claim
- **WHEN** the same job sends the same base with a different address set
- **THEN** the server checks the new set and stores a new claim ID when disjoint

### Requirement: Claim audit record

When the server accepts a claim, the server SHALL write one audit entry with the claim ID and the job ID when present.

#### Scenario: Acceptance recorded
- **WHEN** the server accepts a claim
- **THEN** the audit log holds one entry with the claim ID, the job ID when present and the base state version

### Requirement: Reject bad workspace or base

When a caller registers a claim on a missing, deleted or locked workspace or with an unknown base state version, the server SHALL answer 404, 410 or 422 and store no claim.

#### Scenario: Unknown workspace or base rejected
- **WHEN** a caller registers a claim on a missing, deleted or locked workspace or with an unknown base
- **THEN** the server answers 404, 410 or 422 and stores no claim

### Requirement: Release and expiry of claim

When the job of an accepted claim ends or its lease expires, the server SHALL mark the claim ended with an end time.

#### Scenario: Job end releases
- **WHEN** the job of an accepted claim ends or fails
- **THEN** the server marks the claim ended with an end time

#### Scenario: Crash expires
- **WHEN** its runner crashes or its lease expires
- **THEN** the server marks the claim ended with an end time
