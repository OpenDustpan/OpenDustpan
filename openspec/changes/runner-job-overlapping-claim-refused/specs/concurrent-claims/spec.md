## ADDED Requirements

### Requirement: Refuse overlapping runner claim

When a runner job registers a claim that names a resource instance of an accepted claim of the same workspace, the server SHALL store the new claim in state refused with the blocking claim ID before any apply starts.

#### Scenario: Overlap with running claim
- **WHEN** job C registers a claim that names a resource instance of accepted claim A of the same workspace
- **THEN** the server stores claim C as refused with the ID of claim A and starts no apply for job C

#### Scenario: Disjoint instances accepted
- **WHEN** job C registers a claim on resource instances that claim A does not name
- **THEN** the server does not refuse claim C for overlap

#### Scenario: Idempotent retry of refusal
- **WHEN** job C retries the registration with the same job ID and claim ID after a refusal or a 503
- **THEN** the server answers the same 409 with the same blocking claim ID and stores no duplicate claim

### Requirement: Name blocking claim

When the server refuses a claim, the server SHALL include the blocking claim ID in the refusal.

#### Scenario: Refusal answer
- **WHEN** the server refuses claim C because of claim A
- **THEN** the 409 answer carries the claim ID of claim A and the overlapping resource address

#### Scenario: Two blockers
- **WHEN** claim C overlaps two accepted claims of the same workspace
- **THEN** the 409 answer carries the earliest accepted blocking claim ID of the same workspace

### Requirement: Blocked job ends without provider calls

When the claim of a runner job is refused, the server SHALL move that job to status blocked.

#### Scenario: Overlapping runner job
- **WHEN** claim C of a runner job is refused while claim A applies
- **THEN** job C ends as blocked and the provider log holds no call from job C

### Requirement: Blocked status is final

When a blocking claim ends, the server SHALL keep the blocked job in status blocked.

#### Scenario: Blocker ends
- **WHEN** claim A ends after job C was blocked by claim A
- **THEN** job C stays blocked and a new job with a new claim is needed

### Requirement: Serialize parallel registrations

While two overlapping registrations run in parallel, the server SHALL accept at most 1 claim of the overlapping set for the workspace.

#### Scenario: Two claims at once
- **WHEN** two runner jobs register overlapping claims of the same workspace in parallel
- **THEN** 1 claim of the overlapping set is accepted for the workspace and the other is refused with the first claim as blocker

### Requirement: Authorize refusal read

When a caller reads a refused claim, the server SHALL check the job read action on the job object.

#### Scenario: Denied refusal reader
- **WHEN** a caller without the job read action reads a refused claim
- **THEN** the server denies the read and reveals neither the blocker ID nor the resource address

### Requirement: Release block at claim end

When an accepted claim ends by completion, failure, or lease loss, the server SHALL release its block.

#### Scenario: Blocking claim completes
- **WHEN** claim A completes
- **THEN** a later claim over the same resource is accepted

#### Scenario: Runner crash
- **WHEN** the runner of claim A crashes and its lease ends
- **THEN** a later claim over the same resource is accepted after the lease loss

### Requirement: Enforce accepted claim at apply

When a runner starts an apply without an accepted claim for its job, the server SHALL deny the apply.

#### Scenario: Apply with refused claim
- **WHEN** a runner with a refused claim starts the apply phase
- **THEN** the server denies the apply with 409 that names the blocking claim

#### Scenario: Stale runner after lease loss
- **WHEN** the runner of an ended claim starts or continues the apply phase
- **THEN** the server denies the apply with 409 and accepts no state from it

### Requirement: Record refusal in audit log

When the server refuses a claim, the server SHALL write an audit entry that names the refused claim and the blocking claim.

#### Scenario: Refusal audit
- **WHEN** the server refuses claim C because of claim A
- **THEN** the audit log holds one entry with both claim IDs and the overlap reason
