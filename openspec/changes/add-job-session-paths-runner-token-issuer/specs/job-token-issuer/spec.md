## ADDED Requirements

### Requirement: Make at claim

When a runner claims a runner phase, the server SHALL store one job token bound to the job ID, the stable unit ID and the claim expiry.

#### Scenario: Make at claim
- **WHEN** a runner claims a runner phase
- **THEN** the server stores one token bound to the job ID, the stable unit ID and the claim expiry

#### Scenario: Rollback
- **WHEN** the claim transaction fails
- **THEN** the server stores no token

#### Scenario: Retry
- **WHEN** a claim is repeated
- **THEN** the earlier token of that claim answers 401 and one live token exists

### Requirement: Step bound

When the step timeout plus grace period of a token passes, the server SHALL end the token.

#### Scenario: Grace bound
- **WHEN** the step timeout plus grace period passes while the claim is renewed
- **THEN** the token answers 401

### Requirement: Refuse no unit

If a claim names no stable unit ID, then the server SHALL refuse the claim with invalid.

#### Scenario: No unit
- **WHEN** a claim names no stable unit ID
- **THEN** the server answers invalid and makes no token

### Requirement: Check unit only

When a step presents a job token, the server SHALL allow it only on the state routes of the unit of its job.

#### Scenario: Own unit
- **WHEN** a step uses its token on its unit
- **THEN** the read proceeds under the starter principal

#### Scenario: Other unit
- **WHEN** a step uses its token on another unit
- **THEN** the server answers 403 with the job token message and changes nothing

#### Scenario: Recreated unit
- **WHEN** the unit was removed and made again with the same name
- **THEN** the old token answers 403

#### Scenario: REST API
- **WHEN** a step uses its token on the REST API
- **THEN** the server answers 403

#### Scenario: Runner key
- **WHEN** a runner key is sent on a tool path
- **THEN** the server answers 403

#### Scenario: Grant lost
- **WHEN** the starter lost its grant after the claim
- **THEN** the server answers 403

### Requirement: End with claim

When the claim of a job token ends, the server SHALL end the token in the same transaction.

#### Scenario: Job end
- **WHEN** the job ends
- **THEN** each later use answers 401

#### Scenario: Takeover
- **WHEN** a takeover replaces the claim
- **THEN** the old token answers 401

#### Scenario: Retire
- **WHEN** the runner is retired
- **THEN** the token answers 401

#### Scenario: Renew
- **WHEN** the claim is renewed before the step bound
- **THEN** the token stays valid until its expiry

### Requirement: Fail closed

If a token is unknown, malformed or past its expiry, then the server SHALL answer 401.

#### Scenario: Unknown token
- **WHEN** a caller presents an unknown token
- **THEN** the server answers 401 with the job token ended message and reads no state

#### Scenario: Expired token
- **WHEN** a caller presents a token past its expiry
- **THEN** the server answers 401

### Requirement: Job ID on job tokens

When the server authenticates a job token, the server SHALL set Principal.JobID.

#### Scenario: Job token
- **WHEN** the server authenticates a job token
- **THEN** the principal carries the job ID

### Requirement: No job ID elsewhere

While the server authenticates a credential that is not a job token, the server SHALL leave Principal.JobID empty.

#### Scenario: Other credential
- **WHEN** the server authenticates a runner key or an access token
- **THEN** the principal carries no job ID
