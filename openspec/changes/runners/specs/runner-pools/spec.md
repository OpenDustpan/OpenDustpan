## ADDED Requirements

### Requirement: Create join token

When a principal with namespace:manage-runners creates a join token, the server SHALL return the secret with prefix dpj_ once and keep only its SHA-256.

#### Scenario: Create token for pool
- **WHEN** principal with namespace:manage-runners creates a join token for one pool
- **THEN** server returns the secret once with prefix dpj_ and keeps only its SHA-256 with 1 hour expiry by default and 7 day maximum

### Requirement: One-use join

When a second join arrives with one used join token, the server SHALL refuse it with error token-used.

#### Scenario: Second join refused
- **WHEN** a second join arrives with one used join token
- **THEN** server answers token-used and stores no second runner

#### Scenario: Expired token refused
- **WHEN** a join arrives with an expired join token
- **THEN** server answers new code token-expired and stores no runner

#### Scenario: Revoked token refused
- **WHEN** a join arrives with a revoked join token
- **THEN** server answers new code token-revoked and stores no runner

#### Scenario: Pool mismatch refused
- **WHEN** a join arrives with a token of another pool
- **THEN** server answers invalid and stores no runner

#### Scenario: Bad challenge refused
- **WHEN** a join carries a bad JoinChallenge signature
- **THEN** server answers bad-proof and stores no runner

### Requirement: Revoke and list tokens

When a principal with namespace:manage-runners revokes an unused join token, the server SHALL mark the token revoked and refuse a later join with that token.

#### Scenario: Revoke unused token
- **WHEN** principal with namespace:manage-runners revokes an unused token
- **THEN** later join with that token is refused

### Requirement: Refuse TFE agent token

When a request creates a TFE agent token, the server SHALL refuse it with a JSON:API error that names POST /pools/{pool}/join-tokens.

#### Scenario: TFE token create refused
- **WHEN** a request creates a TFE agent token
- **THEN** server answers a JSON:API error that names POST /pools/{pool}/join-tokens

### Requirement: Claim both tools

When Pulumi jobs and OpenTofu jobs wait in one pool, the server SHALL assign each job through poll to a runner of that pool.

#### Scenario: Pulumi and OpenTofu jobs claimed
- **WHEN** Pulumi and OpenTofu jobs wait in one pool
- **THEN** server assigns each job to the polling runner of the pool

#### Scenario: Concurrent polls
- **WHEN** two runners poll at once for one queued job
- **THEN** server assigns the job to one runner and the other poll gets no work

### Requirement: Unmatched labels stay queued

When a job asks for labels that no runner has, the server SHALL keep the job in state queued.

#### Scenario: No runner matches
- **WHEN** a job asks for labels that no runner has
- **THEN** job stays queued and the job read lists each requested label that no runner reports

### Requirement: Lease expiry and takeover

When the claim lease of a dead runner expires, the server SHALL return the claim to the queue for takeover by another runner.

#### Scenario: Runner killed
- **WHEN** a runner dies during a job and its 60 second lease expires
- **THEN** another runner claims the same job and audit records the takeover

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

When a runner polls with no waiting work, the server SHALL try Claim at once, then hold the poll up to 50 seconds and recheck at least every 5 seconds.

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

### Requirement: Heartbeat and rotation

When a runner sends a heartbeat, the server SHALL answer cancelled claims, dead claims, drain state and rotateKey state.

#### Scenario: Heartbeat lists state
- **WHEN** a runner sends a heartbeat every 15 seconds
- **THEN** answer lists cancelled claims, dead claims, drain flag and rotateKey for a key older than 25 days

#### Scenario: Session open renew
- **WHEN** the runner renews after the launcher opens its session
- **THEN** runner renew answers sessionOpen true and stops renewing

#### Scenario: Cancel renew
- **WHEN** a runner renews during a cancel
- **THEN** renew answers canceled true

### Requirement: Request limits

When a runner request misses Dustpan-Release or exceeds 4 MiB, the server SHALL refuse it with error invalid.

#### Scenario: Release mismatch refused
- **WHEN** a runner request misses or mismatches Dustpan-Release
- **THEN** server answers invalid with no state change

#### Scenario: Oversize body refused
- **WHEN** a runner JSON body exceeds 4 MiB
- **THEN** server answers invalid with no state change

#### Scenario: Wrong pool key refused
- **WHEN** a runner uses a key of another pool
- **THEN** server answers bad-proof with no claim

### Requirement: Live log read stream

When a principal with unit:read-logs reads the log of a running job, the server SHALL stream stored chunks in chunk order while the job runs.

#### Scenario: Read while running
- **WHEN** a principal with unit:read-logs reads the log of a running job
- **THEN** log read route with action unit:read-logs streams stored chunks in order as JSON lines

#### Scenario: Resume after disconnect
- **WHEN** a log reader disconnects and reads again with its last chunk number
- **THEN** server resumes from the next unseen chunk in order
