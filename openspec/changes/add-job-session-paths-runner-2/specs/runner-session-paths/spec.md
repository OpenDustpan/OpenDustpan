## ADDED Requirements

### Requirement: Session exchange and renew

When the launcher exchanges a live claim, the server SHALL open one job session and move the renewal of the claim to the session renew path.

#### Scenario: Exchange then runner renew
- **WHEN** the launcher exchanges the handoff token and the runner then renews the claim
- **THEN** the runner renew answers sessionOpen true and does not renew the claim

### Requirement: Credential split

If a request on a session path carries a runner key proof, then the server SHALL answer 401 bad-proof.

#### Scenario: Runner key on session path
- **WHEN** a runner key proof signs a session renew request
- **THEN** the server answers 401 bad-proof and changes nothing

#### Scenario: Session key on runner path
- **WHEN** a session key proof signs a poll request
- **THEN** the server answers 401 bad-proof

### Requirement: Job purpose only

If the pool of the claim has the sweep purpose, then the server SHALL refuse a job session for that claim.

#### Scenario: Sweep pool
- **WHEN** a runner of a pool with the sweep purpose asks for a job session
- **THEN** the server refuses the session and opens none

### Requirement: Source read

When the launcher reads source through a live session, the server SHALL serve the snapshot of the commit of the job.

#### Scenario: Snapshot not ready
- **WHEN** the snapshot is still in build
- **THEN** the server answers source-not-ready with Retry-After

#### Scenario: Snapshot failed
- **WHEN** the snapshot failed or an LFS object is absent
- **THEN** the phase fails with the reason of the fetch, or the server answers not found for the object

### Requirement: Credentials

When the launcher calls the credentials path on a live session, the server SHALL answer the job token of that claim.

#### Scenario: Token of the claim
- **WHEN** the launcher calls the credentials path
- **THEN** the server answers one token limited to the unit with the expiry of the claim

### Requirement: Fenced step writes

While the claim is live, the server SHALL accept step begin, log chunks and step end only through the session of that claim.

#### Scenario: Write after takeover
- **WHEN** a takeover replaced the claim and the launcher sends a log chunk
- **THEN** the server answers claim-lost and stores nothing

### Requirement: Log chunk check

If a log chunk breaks the size cap, the sequence rule or the hash check, then the server SHALL refuse it and keep no bytes from it.

#### Scenario: Wrong hash
- **WHEN** the body hash differs from Dustpan-Content-SHA256
- **THEN** the server refuses the chunk and the sequence does not move

#### Scenario: Crash after bytes
- **WHEN** the server stops after it stored the bytes and before it moved the counter, and the launcher sends the same chunk again
- **THEN** the server accepts the chunk once and counts it once

### Requirement: Plan commit once

When the launcher commits the plan, the server SHALL store the commit exactly once.

#### Scenario: Second commit
- **WHEN** the launcher commits the plan twice
- **THEN** the second answer is 409 plan-committed and the first commit stays

#### Scenario: Part caps
- **WHEN** the launcher uploads a ninth part or a part above 256 MiB
- **THEN** the server refuses the part and stores nothing
