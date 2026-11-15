## ADDED Requirements

### Requirement: The slow CAS fake never sends an empty answer

The fake CAS server `AnswerSlow` SHALL end the connection without an answer when the client ends the request, and SHALL NOT send a status 200 with an empty body.

#### Scenario: The client gives up
- **WHEN** the client ends the request before the delay of `AnswerSlow` ends
- **THEN** the server sends no status line and no body

### Requirement: An empty CAS answer is not a valid answer

The CAS method SHALL return `session.ErrMethodAnswer` for a status 200 with an empty body.

#### Scenario: Empty answer
- **WHEN** the CAS server answers status 200 with an empty body
- **THEN** the method returns `session.ErrMethodAnswer`
