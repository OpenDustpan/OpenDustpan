## ADDED Requirements

### Requirement: Make at the launcher request

When the launcher of a live claim asks for the credentials of a step through its job session, the server SHALL make one job token bound to the job ID, the stable unit ID and the claim, and answer its plaintext once.

#### Scenario: Make at the request
- **WHEN** the launcher asks for the credentials of a step on a live session
- **THEN** the server stores one token bound to the job ID, the stable unit ID and the claim, and answers the plaintext once

#### Scenario: Claim makes none
- **WHEN** a runner claims a runner phase
- **THEN** the server makes no job token and the claim answer holds none

#### Scenario: Rollback
- **WHEN** the transaction of the issue fails
- **THEN** the server stores no token and writes no audit entry

#### Scenario: Sweep
- **WHEN** the launcher of a sweep asks for credentials
- **THEN** the answer holds no job token

### Requirement: Expiry

The server SHALL set the expiry of a job token to the earlier of the end of the claim and the end of the step timeout plus the grace period.

#### Scenario: Claim ends first
- **WHEN** the claim ends before the step timeout plus grace
- **THEN** the expiry is the end of the claim

#### Scenario: Step bound first
- **WHEN** the step timeout plus grace ends before the claim
- **THEN** the expiry is the end of the step timeout plus grace and the token answers 401 after it, while the claim is renewed

### Requirement: One audit entry per request

The server SHALL write one audit entry for each request of the launcher that makes a token, and the entry SHALL name the job, the step and the expiry.

#### Scenario: Entry
- **WHEN** the server makes a token at a request
- **THEN** one audit entry names the job, the step and the expiry

#### Scenario: Refused request after the proof check
- **WHEN** the proof check passes and the server then refuses the request because the claim ended between the checks, the job names no stable unit ID, the starter lost its grant or is gone, or the launcher is a sweep
- **THEN** it makes no token, writes no issue entry, and writes one denial audit entry that names the job, the step and the reason

#### Scenario: Refused request before the proof check
- **WHEN** the server refuses the request before the proof check because the claim was lost (409), the session is retired (403), or the session store decides with no row
- **THEN** it makes no token, writes no audit entry, and writes one log record that holds no secret, the session ID shortened, and the reason

#### Scenario: Denial entry holds no secret
- **WHEN** the server writes a denial audit entry
- **THEN** the entry holds the job ID, the step name and a reason code, and holds no token text and no proof

#### Scenario: Failed transaction
- **WHEN** the transaction of the issue fails after the checks pass
- **THEN** the server stores no token and writes no issue entry, and the answer is an error with no token

### Requirement: Refuse no unit

If a job names no stable unit ID, then the server SHALL refuse the request with invalid and make no token.

#### Scenario: No unit
- **WHEN** the job of the request names no stable unit ID
- **THEN** the server answers invalid and makes no token

### Requirement: Refuse a dead claim

If the claim of the session is not live, then the server SHALL refuse the request and make no token.

#### Scenario: Claim ended
- **WHEN** the launcher asks after the job ended, the claim expired, a takeover replaced the claim or the runner was retired
- **THEN** the server refuses and makes no token

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
- **WHEN** the starter lost its grant after the request
- **THEN** the server answers 403

### Requirement: End with claim

While a job token is in use, the server SHALL read the claim, so that a token stops when its claim ends.

#### Scenario: Job end
- **WHEN** the job ends
- **THEN** each later use answers 401

#### Scenario: Takeover
- **WHEN** a takeover replaces the claim
- **THEN** the old token answers 401

#### Scenario: Retire
- **WHEN** the runner is retired
- **THEN** the token answers 401

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
