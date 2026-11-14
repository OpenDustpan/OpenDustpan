## ADDED Requirements

### Requirement: api-only-skips-claim-loop

While the server runs only the api component, the server SHALL exclude the claim-loop check from readiness.

#### Scenario: api-only-stays-ready
- **WHEN** the server runs only the api component with a durable store and a fake clock past 60 s
- **THEN** GET /health/ready answers 200 after more than 2 claim intervals with no claim loop run.

### Requirement: worker-keeps-claim-loop

While the server runs the worker component with a durable store, the server SHALL fail readiness when the claim loop never runs.

#### Scenario: worker-reports-not-ready
- **WHEN** the server runs the worker component with a durable store and Run was never called
- **THEN** GET /health/ready answers 503 naming claim-loop after 2 claim intervals.

#### Scenario: full-server-reports-not-ready
- **WHEN** the server runs all components with a durable store and the claim loop never runs
- **THEN** GET /health/ready answers 503 naming claim-loop after 2 claim intervals.

### Requirement: no-store-skips-claim-loop

While the server runs with no durable store, the server SHALL exclude the claim-loop check from readiness.

#### Scenario: no-store-stays-ready
- **WHEN** the server runs any component mix with no durable store
- **THEN** GET /health/ready answers 200 after more than 2 claim intervals with no claim loop run.

### Requirement: startup-grace

While less than 2 claim intervals passed since Start, the server SHALL exclude the claim-loop failure from readiness.

#### Scenario: startup-stays-ready
- **WHEN** the server runs the worker component with a durable store and less than 2 claim intervals passed since Start
- **THEN** GET /health/ready answers 200.
