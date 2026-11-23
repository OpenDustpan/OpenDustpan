## ADDED Requirements

### Requirement: Session exchange

When the launcher exchanges a live claim, the server SHALL open one job session and move the renewal of the claim to the session renew path.

#### Scenario: Exchange then runner renew
- **WHEN** the launcher exchanges the handoff token and the runner then renews the claim
- **THEN** the runner renew answers sessionOpen true and does not renew the claim

#### Scenario: Reused handoff token
- **WHEN** the launcher exchanges a handoff token a second time
- **THEN** the server answers bad-proof and opens no second session

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

### Requirement: Cancel on renew

When a cancel is requested for the claim, the server SHALL answer canceled true on the session renew.

#### Scenario: Cancel seen
- **WHEN** a cancel is requested and the launcher renews
- **THEN** the renew answers canceled true

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

#### Scenario: Out of order
- **WHEN** the launcher sends a sequence number that skips one
- **THEN** the server refuses the chunk

### Requirement: Session caps

If an upload passes a session cap, then the server SHALL refuse it with the code invalid.

#### Scenario: Step chunk cap
- **WHEN** the launcher sends chunk 20001 of one step
- **THEN** the server refuses it

#### Scenario: Step byte cap
- **WHEN** the log of one step passes 256 MiB
- **THEN** the server refuses the chunk and keeps no bytes from it

#### Scenario: Phase chunk cap
- **WHEN** the launcher sends chunk 100001 of one phase
- **THEN** the server refuses it

#### Scenario: Phase byte cap
- **WHEN** the logs of one phase pass 1 GiB
- **THEN** the server refuses the chunk and keeps no bytes from it

#### Scenario: Body cap
- **WHEN** a JSON body is above 4 MiB
- **THEN** the server refuses it

### Requirement: Plan commit

When the launcher commits the plan, the server SHALL store the plan once and answer a repeat of the same commit with 204.

#### Scenario: Same commit again
- **WHEN** the launcher commits the same parts with the same hashes under the same claim a second time
- **THEN** the server answers 204 and changes nothing

#### Scenario: Other parts after a commit
- **WHEN** the launcher commits other parts after a commit stored the plan
- **THEN** the server answers conflict and the first plan stays

#### Scenario: Part caps
- **WHEN** the launcher commits more than 8 parts or a part above 256 MiB
- **THEN** the server refuses the commit and stores nothing
