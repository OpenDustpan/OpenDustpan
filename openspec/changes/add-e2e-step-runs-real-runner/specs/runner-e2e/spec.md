## ADDED Requirements

### Requirement: Real runner plan and apply

When a pinned real runner joins a pool with a join token, the server SHALL let it claim a plan job and an apply job and record each report.

#### Scenario: Plan then apply on a real runner
- **WHEN** a pinned real runner with a local launcher claims a plan job and an apply job
- **THEN** the plan ends succeeded, the apply ends succeeded, and the job links to the state write.

### Requirement: Cancel drain retire with no retry mask

When the operator cancels a job and then drains and retires its runner, the server SHALL stop the job and end the runner without a hidden retry.

#### Scenario: Cancel then drain then retire
- **WHEN** the operator cancels a job and then drains and retires its runner
- **THEN** the canceled job stops, the drained runner takes no new claim, and the retired runner leaves the pool.

### Requirement: Runner refusals

When a runner call is forbidden, unknown, or conflicts with an open claim, the server SHALL refuse it with the documented error code.

#### Scenario: List without grant
- **WHEN** a caller without the pool read grant lists pools
- **THEN** the server answers 403 with code runner.forbidden and names the missing read action.

#### Scenario: Join with bad token
- **WHEN** a runner joins with a revoked or unknown join token
- **THEN** the server answers 404 with code runner.no-such-token.

#### Scenario: Retire a busy runner
- **WHEN** the operator retires a runner that still holds a claim
- **THEN** the server answers 409 with code runner.runner-busy and names the drain command.

### Requirement: Claim expiry after a crash

When a runner that holds a claim stops without a report, the server SHALL expire the claim at lease end and offer the job again.

#### Scenario: Runner lost during a job
- **WHEN** a runner that holds a claim stops without a report
- **THEN** the claim expires and another runner takes the job, and the log shows the handoff.

### Requirement: Authorization actions on runner routes

When the e2e flow drives runner routes, the server SHALL check the read action on each list and the manage action on each drain and retire.

#### Scenario: Read and manage actions
- **WHEN** the e2e flow drives list, drain and retire with a least-privilege caller
- **THEN** the list needs the read action and drain and retire need the manage action, and a caller without each action gets 403.

### Requirement: Idempotency on mutating runner calls

When the operator repeats a mutating runner call with the same Idempotency-Key, the server SHALL return the stored answer.

#### Scenario: Replay with the same key
- **WHEN** the operator repeats a join-token, drain or retire call with the same Idempotency-Key
- **THEN** the server returns the stored answer and changes nothing twice.

### Requirement: Matrix and cleanup

When the real-runner flow runs on any store, the server SHALL pass the same steps and remove the pool and the runner after the run.

#### Scenario: Same flow on both stores over TLS
- **WHEN** the flow runs on SQLite and on PostgreSQL over TLS against the real binary
- **THEN** plan, apply, cancel, drain and retire pass on each store with no manual step.

#### Scenario: Teardown after failure
- **WHEN** the flow fails at any step
- **THEN** the pool and the runner are gone and the next run starts clean.
