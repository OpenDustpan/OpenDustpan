## ADDED Requirements

### Requirement: Drain on SIGTERM

When SIGTERM arrives, the server SHALL drain through the existing drain period and answer 503 with a Retry-After header on readiness.

#### Scenario: The stop signal drains readiness
- **WHEN** SIGTERM arrives at a serving instance.
- **THEN** readiness answers 503 and new durable claims are refused while in-flight requests complete up to the timeout.

#### Scenario: Retry-After on new durable starts
- **WHEN** a new durable start arrives while draining.
- **THEN** the server answers 503 with a Retry-After header.

#### Scenario: Healthy instance serves
- **WHEN** no drain is active.
- **THEN** readiness answers 200 and claims proceed.

### Requirement: Release claims after drain

When drain ends by timeout or empty in-flight set, the server SHALL release held durable claims via ReleaseOnShutdown for takeover.

#### Scenario: ReleaseOnShutdown takes over backup
- **WHEN** a draining instance held a backup claim at SIGTERM and ReleaseOnShutdown runs.
- **THEN** another worker instance claims and completes the backup.

#### Scenario: ReleaseOnShutdown falls back to lease expiry
- **WHEN** a second SIGTERM or a store outage stops the release.
- **THEN** the claim stays pending until lease expiry and another worker claims it within 60 s.
