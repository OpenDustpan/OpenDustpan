## ADDED Requirements

### Requirement: research-note-fail-closed

The research note SHALL state the fail-closed behavior for each cost gate option when no estimate arrived.

#### Scenario: missing-estimate-fail-closed
- **WHEN** no estimate arrived before change.planned
- **THEN** the note states the gate sees cost.available false and fails closed or asks for approval, and names the available check

#### Scenario: late-estimate-after-wait
- **WHEN** the estimate arrives after the wait
- **THEN** the note states the plan waits at most 15 minutes then proceeds with cost.available false on push data only
