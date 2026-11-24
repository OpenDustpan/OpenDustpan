## ADDED Requirements

### Requirement: Poll loop

While a runner runs, the runner loop SHALL poll the existing route `POST /runner/v1/poll` with the runner key, and poll again after a 204 answer.

#### Scenario: Work waits
- **WHEN** The loop polls and work of its pool waits.
- **THEN** The answer holds one assignment and the loop starts the work.

#### Scenario: No work
- **WHEN** The loop polls and no work waits.
- **THEN** The server answers 204 after the wait and the loop polls again.

#### Scenario: Another pool
- **WHEN** A runner of another pool polls while work waits in this pool.
- **THEN** The server answers 204 and the work stays queued.

#### Scenario: Retired runner
- **WHEN** A retired runner polls.
- **THEN** The server answers 403 with `retired`, the loop stops and no work starts.

#### Scenario: Bad proof
- **WHEN** A poll carries a bad or replayed proof.
- **THEN** The server answers 401 with `bad-proof` and the loop stops.

#### Scenario: Rate limit
- **WHEN** The server answers 429 with `rate-limited`.
- **THEN** The loop waits for `Retry-After` and polls again.

### Requirement: Renew the claim by the pool lease

While the loop holds a claim, it SHALL renew the claim through the existing route `POST /runner/v1/claims/{operation}/renew` after a third of the lease of the pool, and SHALL send the existing heartbeat every 15 s.

#### Scenario: Holder renews
- **WHEN** The loop renews before expiry.
- **THEN** The server moves the expiry to now plus the lease of the pool.

#### Scenario: Lease of the pool
- **WHEN** A pool sets no lease.
- **THEN** The lease is 90 s, and the pool shows that effective value in the API.

#### Scenario: Lost claim
- **WHEN** The renew answer is 409 with `claim-lost`.
- **THEN** The loop stops the work at once and reports nothing more.

### Requirement: Fence claim writes

When a claim write carries a stale fence, the server SHALL deny the write and keep the stored claim state.

#### Scenario: Fence moved
- **WHEN** A claim write carries a stale fence.
- **THEN** The server denies the write and keeps the stored claim state.
