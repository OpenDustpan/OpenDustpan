## ADDED Requirements

### Requirement: RecordFailure effects

When RecordFailure runs for a missing, expired, or non-open challenge, the server SHALL return ErrNotFound for a missing challenge, ErrExpired for an expired challenge, and ErrUsed for a non-open challenge with no failure-count change and no audit append by RecordFailure itself.

#### Scenario: Missing challenge
- **WHEN** RecordFailure runs for a missing challenge
- **THEN** the call returns ErrNotFound with no failure-count change and no audit append by RecordFailure

#### Scenario: Expired challenge
- **WHEN** RecordFailure runs for an expired challenge
- **THEN** the call returns ErrExpired with no failure-count change and no audit append by RecordFailure

#### Scenario: Non-open challenge
- **WHEN** RecordFailure runs for a used or otherwise non-open challenge
- **THEN** the call returns ErrUsed with no failure-count change and no audit append by RecordFailure

### Requirement: CreateChallenge kinds

When CreateChallenge runs with an empty or unsupported holder kind, the server SHALL return ErrInvalid and store no challenge.

#### Scenario: Rejected kinds
- **WHEN** CreateChallenge runs with an empty kind and with an unsupported kind
- **THEN** each call returns ErrInvalid and no challenge is stored

#### Scenario: Supported kinds
- **WHEN** CreateChallenge runs with holder kind session and with holder kind access-token
- **THEN** each call stores a challenge

### Requirement: Single audit layer

When the step-up service refuses a request, the server SHALL append exactly one audit entry from the service for ErrNotFound, ErrExpired, and ErrUsed and from the store for ErrMismatch and ErrFactorRefused.

#### Scenario: Five refusals
- **WHEN** each of ErrNotFound, ErrExpired, ErrUsed, ErrMismatch, and ErrFactorRefused occurs once
- **THEN** each case has exactly one audit entry with no duplicate

#### Scenario: RFC assignment
- **WHEN** a reader opens the step-up refusal section of RFC 102
- **THEN** the RFC text names the service for the first three errors and the store for the last two

### Requirement: TOTP reservation

When AcceptStep runs with a reservation that BeginTOTPAttempt did not record, the server SHALL refuse the attempt and keep the factor state unchanged.

#### Scenario: Fabricated reservation
- **WHEN** AcceptStep runs with a reservation that BeginTOTPAttempt did not record
- **THEN** the attempt is refused and the factor state is unchanged

#### Scenario: Reuse with higher step
- **WHEN** AcceptStep runs twice with one reservation and a higher step on the second use
- **THEN** the second use is refused and the factor state is unchanged

#### Scenario: Concurrent consume
- **WHEN** two callers consume one reservation at the same time
- **THEN** one use succeeds and the other is refused with no double count

### Requirement: Key-add binding

When AddKey or EnrollFirstKey succeeds, the server SHALL mark its key-add challenge answered in the same transaction as the key insertion and the audit append.

#### Scenario: Reuse after revocation
- **WHEN** the same key-add challenge is used again after the key is revoked but before challenge expiry
- **THEN** the second use is refused

#### Scenario: Failed enrollment
- **WHEN** AddKey or EnrollFirstKey fails
- **THEN** the challenge stays unconsumed and no partial key or audit change remains

#### Scenario: Concurrent reuse
- **WHEN** two callers use one key-add challenge at the same time
- **THEN** one enrollment succeeds and the other is refused

### Requirement: Atomic reset

When an administrator resets the step-up factors of a person, the server SHALL delete the step-up keys and the active and pending TOTP factors, revoke the step-up credentials, and append one reset audit entry in one transaction.

#### Scenario: Complete reset
- **WHEN** an administrator resets the step-up factors of a person
- **THEN** all step-up keys, active and pending TOTP factors, and step-up credentials of the person are gone and exactly one reset audit entry exists

#### Scenario: Injected failure
- **WHEN** the reset transaction fails
- **THEN** factors, credentials, and audit entries are unchanged

### Requirement: Errcode precedence

When a request has a session error and identity.ErrDenied, the server SHALL report the session error.

#### Scenario: CSRF versus denied
- **WHEN** a request has both a CSRF failure and a denial
- **THEN** the session error takes precedence over identity.ErrDenied

#### Scenario: Other-site versus denied
- **WHEN** a request has both an other-site failure and a denial
- **THEN** the session error takes precedence over identity.ErrDenied

### Requirement: CheckAccess propagation

When CheckAccess returns an unexpected error, the server SHALL propagate that error without adding identity.ErrDenied.

#### Scenario: Unexpected read error
- **WHEN** CheckAccess fails with an unexpected store or policy error
- **THEN** the caller sees the original error with no added ErrDenied

#### Scenario: Access-limit refusal
- **WHEN** CheckAccess refuses under an access limit
- **THEN** the denial keeps its access-limit classification
