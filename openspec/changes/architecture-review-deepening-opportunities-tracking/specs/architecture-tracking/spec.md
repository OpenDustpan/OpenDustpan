## ADDED Requirements

### Requirement: Index links

The index SHALL link each finding to its own follow-up change or RFC.

#### Scenario: Index-drift-check
- **WHEN** the drift check re-runs the evidence of each finding on current main
- **THEN** each finding links to its own follow-up or RFC and each stale line reference is flagged

### Requirement: No behavior change

The server SHALL keep its behavior unchanged by this tracking change.

#### Scenario: No-runtime-change
- **WHEN** this change is applied
- **THEN** no route, wire value, audit entry or stored format differs from main
