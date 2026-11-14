## ADDED Requirements

### Requirement: Skip counter

When a runner poll skips a job at the bound of candidate jobs, the server SHALL add the number of skipped jobs to the skip counter.

#### Scenario: Queue over the bound
- **WHEN** a pool has 70 matching queued jobs and a runner polls
- **THEN** the skip counter increases by 6

#### Scenario: Queue under the bound
- **WHEN** a pool has 10 queued jobs and a runner polls
- **THEN** the skip counter does not change

### Requirement: Counter on the metrics route

The server SHALL list the skip counter on the metrics route.

#### Scenario: Fresh server
- **WHEN** an operator reads the metrics route after a start
- **THEN** the skip counter shows 0
