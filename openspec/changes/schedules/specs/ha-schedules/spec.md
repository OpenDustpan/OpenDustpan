## ADDED Requirements

### Requirement: Single runner per tick

When a schedule is due, the server SHALL run ClaimDue with one conditional UPDATE that moves next_run_at forward by one interval.

#### Scenario: Two instances poll a due schedule with ClaimDue
- **WHEN** ClaimDue runs on two instances for one due schedule on a shared database
- **THEN** exactly one instance wins the claim

### Requirement: One row per tick

When a tick starts, the server SHALL write at most 1 durable_operations row for the tick start key.

#### Scenario: Sixty second run
- **WHEN** the exit test reads durable_operations after 60 s at a 10 s interval
- **THEN** each tick start key has 1 row at most

### Requirement: Missed ticks collapse

If next_run_at lies in the past, then the server SHALL run one tick for the missed interval.

#### Scenario: Ticks missed in an outage
- **WHEN** next_run_at lies in the past
- **THEN** the next claim runs one tick

### Requirement: Takeover after holder death

When the holder of a tick operation dies, the server SHALL hand the operation over only after its 30 s expiry with a higher fencing token.

#### Scenario: Holder dies mid-run
- **WHEN** the holder of the tick operation dies
- **THEN** another instance claims after the 30 s expiry with a higher fencing token and the stale write fails

### Requirement: Store down stays due

When the database is not available, the server SHALL answer 503 with a Retry-After header in the shape of the API.

#### Scenario: Claim during outage
- **WHEN** the database is not available at the claim moment
- **THEN** the API answers 503 with Retry-After and the tick stays due
