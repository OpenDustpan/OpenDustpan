## ADDED Requirements

### Requirement: join-with-token

When a runner joins with a join token, the server SHALL admit the join once per unused token.

#### Scenario: Plan then apply on a real runner
- **WHEN** a pinned real runner with a local launcher claims a plan job and an apply job
- **THEN** the plan ends succeeded, the apply ends succeeded, and the job links to the state write.

#### Scenario: Join with bad token
- **WHEN** a runner joins with a revoked or unknown join token
- **THEN** the server answers 404 with the no-such-token code and adds no runner.

### Requirement: drain-retire

When the operator drains and retires a runner, the server SHALL stop new claims and end the runner without a hidden retry.

#### Scenario: Cancel then drain then retire
- **WHEN** the operator cancels a job and then drains and retires its runner
- **THEN** the canceled job stops, the drained runner takes no new claim, and the retired runner leaves the pool.

#### Scenario: Drain race with in-flight claim
- **WHEN** drain arrives while a claim starts
- **THEN** the open claim finishes and the drained runner takes no new claim.

#### Scenario: Retire a busy runner
- **WHEN** the operator retires a runner that still holds a claim
- **THEN** the server answers 409 with the runner-busy code.

#### Scenario: Cancel while runner is gone
- **WHEN** the operator cancels a running job whose runner stopped without a report
- **THEN** the job returns to queued and a second runner claims it once.

### Requirement: Claim expiry after a crash

When a runner that holds a claim stops without a report, the server SHALL expire the claim at lease end and offer the job again.

#### Scenario: Runner lost during a job
- **WHEN** a runner that holds a claim stops without a report
- **THEN** the claim expires at lease end and another runner takes the job, and the log shows the handoff.

### Requirement: Authorization actions on runner routes

When the e2e flow drives runner routes, the server SHALL check the read action on each list and the manage action on each drain and retire.

#### Scenario: List without grant
- **WHEN** a caller without the pool read grant lists pools
- **THEN** the server answers 403 with the runner forbidden code.

#### Scenario: Read and manage actions
- **WHEN** the e2e flow drives list, drain and retire with a least-privilege caller
- **THEN** the list needs the read action and drain and retire need the manage action, and a caller without each action gets 403.

### Requirement: Idempotency on drain and retire

When the operator repeats a drain or retire call with the same Idempotency-Key, the server SHALL return the stored answer.

#### Scenario: Replay with the same key
- **WHEN** the operator repeats a drain or retire call with the same Idempotency-Key
- **THEN** the server returns the stored answer and changes nothing twice.

### Requirement: Matrix and cleanup

When the real-runner flow runs on any store, the server SHALL pass the same steps and remove the pool and the runner after the run.

#### Scenario: Same flow on both stores over TLS
- **WHEN** the flow runs on SQLite and on PostgreSQL over TLS against the real binary
- **THEN** plan, apply, cancel, drain and retire pass on each store with no manual step.

#### Scenario: Teardown after failure
- **WHEN** the flow fails at any step
- **THEN** the pool and the runner are gone and the next run starts clean.
