## ADDED Requirements

### Requirement: Create join token

When a principal with namespace:manage-runners creates a join token, the server SHALL return the secret with prefix dpj_ once and keep only its SHA-256.

#### Scenario: Create token for pool
- **WHEN** principal with namespace:manage-runners creates a join token for one pool
- **THEN** server returns the secret once and keeps only its SHA-256 with expiry

### Requirement: One-use join

When a second join arrives with one used join token, the server SHALL refuse it with error token-used.

#### Scenario: Second join refused
- **WHEN** a second join arrives with one used join token
- **THEN** server answers token-used and stores no second runner

#### Scenario: Expired token refused
- **WHEN** a join arrives with an expired join token
- **THEN** server answers token-expired and stores no runner

#### Scenario: Revoked token refused
- **WHEN** a join arrives with a revoked join token
- **THEN** server answers token-revoked and stores no runner

### Requirement: Refuse TFE agent token

When a request creates a TFE agent token, the server SHALL refuse it with a JSON:API error that names the pool join-token route.

#### Scenario: TFE token create refused
- **WHEN** a request creates a TFE agent token
- **THEN** server answers a JSON:API error that names the pool join-token route

### Requirement: Claim both tools

When Pulumi jobs and OpenTofu jobs wait in one pool, the server SHALL assign each job through poll to a runner of that pool.

#### Scenario: Pulumi and OpenTofu jobs claimed
- **WHEN** Pulumi and OpenTofu jobs wait in one pool
- **THEN** server assigns each job to the polling runner of the pool

### Requirement: Unmatched labels stay queued

When a job asks for labels that no runner has, the server SHALL keep the job in state queued.

#### Scenario: No runner matches
- **WHEN** a job asks for labels that no runner has
- **THEN** job stays queued and the job read lists each requested label that no runner reports

### Requirement: Lease expiry and takeover

When the claim lease of a dead runner expires, the server SHALL return the claim to the queue for takeover by another runner.

#### Scenario: Runner killed
- **WHEN** a runner dies during a job and its lease expires
- **THEN** another runner claims the same job and continues the phase

#### Scenario: Second expiry after takeover
- **WHEN** the takeover runner dies before it renews the claim
- **THEN** claim returns to the queue again for a third runner

#### Scenario: Stale owner renew refused
- **WHEN** the first owner returns after takeover and renews the lost claim
- **THEN** server refuses the renew and keeps the lease with the takeover owner

#### Scenario: Retired live claim expires
- **WHEN** a runner is retired while its claim is still live
- **THEN** claim returns to the queue and no renew revives it

### Requirement: Must rejoin after long offline

When a runner with an expired key polls, the server SHALL answer must-rejoin and assign no work.

#### Scenario: Expired key polls
- **WHEN** a runner offline more than 30 days polls with an expired key
- **THEN** server answers must-rejoin and assigns no work

### Requirement: Retired runner refused

When a retired runner polls or renews a claim, the server SHALL refuse the request with error retired.

#### Scenario: Retired runner polls
- **WHEN** a retired runner polls or renews a claim
- **THEN** server refuses the poll and the renew with error retired

#### Scenario: Retired session request refused
- **WHEN** a job session of a retired runner sends a later request
- **THEN** server refuses the session request with error retired

### Requirement: Proof replay refused

When a runner poll repeats a proof nonce or carries a stale time, the server SHALL answer bad-proof and assign no work.

#### Scenario: Replayed proof
- **WHEN** a poll repeats a proof nonce or carries a stale time
- **THEN** server answers bad-proof and assigns no work

### Requirement: Base long poll wait

When a runner polls with no waiting work, the server SHALL hold the poll up to 50 seconds and recheck at least every 5 seconds.

#### Scenario: Work arrives during wait
- **WHEN** a job starts while a runner poll waits
- **THEN** waiting poll returns the new work without a second poll

#### Scenario: No work in wait window
- **WHEN** no job starts within the 50 second wait
- **THEN** poll ends with no work and the runner polls again

### Requirement: Renew owner check

When a runner renews a claim owned by another runner, the server SHALL refuse the renew.

#### Scenario: Renew by other runner
- **WHEN** a runner renews a claim owned by another runner
- **THEN** server refuses the renew and keeps the lease with its owner

### Requirement: Live log read stream

When a principal reads the log of a running job, the server SHALL stream stored chunks in chunk order while the job runs.

#### Scenario: Read while running
- **WHEN** a principal reads the log of a running job
- **THEN** API streams stored chunks in order while the job still appends

#### Scenario: Resume after disconnect
- **WHEN** a log reader disconnects and reads again with its last chunk number
- **THEN** server resumes from the next unseen chunk in order

#### Scenario: Stream closes at end
- **WHEN** a job ends or is cancelled while a log stream is open
- **THEN** open stream ends cleanly after the last chunk
