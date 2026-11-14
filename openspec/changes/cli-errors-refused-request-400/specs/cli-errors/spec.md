## ADDED Requirements

### Requirement: No retry hint for refused requests

When the server answers with a 4xx status, the CLI SHALL omit the run again hint.

#### Scenario: Duplicate namespace
- **WHEN** namespace create answers 409 for an existing name
- **THEN** the CLI prints the server message once with no run again hint

#### Scenario: Invalid name
- **WHEN** namespace create answers 400 for an invalid name
- **THEN** the CLI prints the server message once with no retry hint

#### Scenario: Server failure keeps hint
- **WHEN** the server answers 500 or 503, or the connection fails
- **THEN** the CLI prints the run again hint

### Requirement: Runner API off answer

When no public URL is set, the server SHALL answer a JSON 404 with the NEW error code runner.off on each runner management route.

#### Scenario: Pools without public URL
- **WHEN** a caller with an access token reads the runner pools route with no public URL set
- **THEN** the server answers 404 JSON that names the public URL setting

#### Scenario: Public URL set
- **WHEN** the public URL is set
- **THEN** the server routes the call to the normal runner handler

### Requirement: Fault cause order

When a blob write fails with ENOSPC, the server SHALL log the NEW cause no_space.

#### Scenario: Full disk
- **WHEN** a blob write fails with ENOSPC
- **THEN** the log names no_space and the client gets 500 internal error
