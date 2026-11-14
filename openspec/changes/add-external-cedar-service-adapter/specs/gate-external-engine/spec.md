## ADDED Requirements

### Requirement: Evaluate through the service

When the installation names an external Cedar service, the server SHALL evaluate each unit through that service.

#### Scenario: Service evaluates
- **WHEN** a change reaches a decision point
- **THEN** the server returns the matched policy IDs and diagnostics for each unit

### Requirement: Bad answer is unavailable

If an answer does not authenticate, does not echo both IDs, or names an unknown policy ID, then the server SHALL give gate.ErrUnavailable.

#### Scenario: Answer without echo
- **WHEN** an answer misses the echoed request ID
- **THEN** the evaluation gives gate.ErrUnavailable

### Requirement: Diagnostic fails closed

When the service returns a Cedar diagnostic for a gate in enforce mode, the server SHALL stop the change with gate.ErrRuleFailed.

#### Scenario: Diagnostic in enforce mode
- **WHEN** the service returns a diagnostic
- **THEN** the change stops and the record holds result error

### Requirement: Start with the service down

When the service is unreachable at boot, the server SHALL start.

#### Scenario: Service down at boot
- **WHEN** the service is unreachable at boot
- **THEN** the server starts and gated calls fail closed per mode
