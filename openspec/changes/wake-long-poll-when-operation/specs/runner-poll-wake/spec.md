## ADDED Requirements

### Requirement: Wake on same-instance start

When a durable operation of a kind of the pool starts on the same instance, the server SHALL publish the kind and wake each waiting poll of that pool.

#### Scenario: Same-kind start wakes poll
- **WHEN** a durable operation of a kind of the pool starts on the same instance while a poll waits
- **THEN** the poll retries Claim and returns the new work within 1 second

#### Scenario: Other kind does not wake
- **WHEN** a durable operation of another kind starts
- **THEN** the poll keeps waiting until the timer or the deadline

#### Scenario: Rollback does not wake
- **WHEN** a start rolls back
- **THEN** the poll keeps waiting until the timer or the deadline

#### Scenario: Thundering herd keeps losers waiting
- **WHEN** many polls of one pool wait and one operation of a kind of the pool starts
- **THEN** one waiting poll gets the work and the rest keep waiting

### Requirement: Claim on subscribe

When a poll starts to wait, the server SHALL subscribe for its pool kinds and run Claimer.Claim at once before it waits on the signal.

#### Scenario: Commit before subscribe
- **WHEN** an operation commits just before a poll subscribes
- **THEN** the poll returns the work without waiting for the signal

### Requirement: Keep five second fallback

While a poll waits, the server SHALL retry Claim at least every 5 seconds for operations that other instances start.

#### Scenario: Other instance start
- **WHEN** an operation starts on another instance
- **THEN** the poll finds the work at the next 5 s tick or before the 50 s deadline

#### Scenario: Lost signal bounded
- **WHEN** the instance-local signal is lost after a start commits
- **THEN** the poll finds the work at the next 5 s tick

#### Scenario: Restart during wait
- **WHEN** the server process restarts while work is queued
- **THEN** a new poll finds the work at once or at the next 5 s tick

### Requirement: One poll at a time

When a runner with one open poll opens a second poll, the server SHALL answer 429 with the existing one-poll code.

#### Scenario: Second concurrent poll refused
- **WHEN** one runner opens a second concurrent poll under the rate
- **THEN** the server answers 429 with the existing one-poll code and the first poll keeps waiting

#### Scenario: Limiter refusal creates no poll
- **WHEN** a runner at its rate limit sends one more poll while one poll waits
- **THEN** the server answers 429 rate-limited and holds no new open poll

### Requirement: Wait lifetime

When the poll request ends or the server shuts down, the server SHALL unsubscribe the wait and leak no goroutine.

#### Scenario: Client end unsubscribes
- **WHEN** the client ends the request or the server shuts down during a wait
- **THEN** the server unsubscribes the poll and leaks no goroutine

### Requirement: Retired runner ends wait

When a runner is retired while its poll waits, the server SHALL end the poll without work.

#### Scenario: Retire during wait
- **WHEN** an administrator retires the runner while its poll waits
- **THEN** the waiting poll ends without work at the next wake or 5 s tick and the next poll fails authentication as retired

### Requirement: Drained runner keeps wait

When a runner is drained while its poll waits, the server SHALL hold the poll without claim until the deadline.

#### Scenario: Drain during wait
- **WHEN** an administrator marks the runner draining while its poll waits
- **THEN** the waiting poll keeps waiting without claim until the 50 s deadline
