## ADDED Requirements

### Requirement: suppressed-refusal-counter

When a handoff refusal audit entry is suppressed by the per-window limiter, the server SHALL increment the runner_handoff_audit_suppressed_total counter by one with zero labels.

#### Scenario: Suppressed audit entry increments counter once
- **WHEN** a handoff refusal audit entry is suppressed by the per-window limiter
- **THEN** the server increments the runner_handoff_audit_suppressed_total counter by one exactly once with zero labels.

#### Scenario: Carried suppressed count does not count twice
- **WHEN** a suppressed count carries to the next audit window
- **THEN** the counter increases by the number of suppressed entries only, not again when the carried count is written.

#### Scenario: Second server reuses counter registration
- **WHEN** a second server is built in the same process
- **THEN** both servers increment the same registered counter without a registration panic.

### Requirement: counter-visible-at-zero

The server SHALL expose the runner_handoff_audit_suppressed_total counter with value zero before the first suppression.

#### Scenario: Counter registered before first suppression
- **WHEN** the server starts before any handoff refusal is suppressed
- **THEN** the metrics route shows the runner_handoff_audit_suppressed_total counter with value zero.

### Requirement: written-entry-does-not-increment

When a handoff refusal audit entry is written without suppression, the server SHALL leave the runner_handoff_audit_suppressed_total counter unchanged.

#### Scenario: Written audit entry leaves counter unchanged
- **WHEN** a handoff refusal audit entry is written without suppression
- **THEN** the runner_handoff_audit_suppressed_total counter stays unchanged.
