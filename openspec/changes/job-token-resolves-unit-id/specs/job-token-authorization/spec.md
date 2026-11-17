## ADDED Requirements

### Requirement: A job token reaches its unit by the name of the route

The server SHALL resolve the stable unit ID of a unit that a route names, before it compares it with the unit of the job token, so that a job token acts on the route of its own unit and on no other unit.

#### Scenario: Own unit by name
- **WHEN** a step uses its job token on the state route of the unit of its job, which the route names by name
- **THEN** the server allows the action of the starter principal on that unit

#### Scenario: Another unit by name
- **WHEN** a step uses its job token on the state route of another unit
- **THEN** the server answers 403 and changes nothing

#### Scenario: Unit that does not exist
- **WHEN** a step uses its job token on a route that names no unit
- **THEN** the server refuses the action and reads or changes nothing

#### Scenario: Port fails
- **WHEN** the read of the stable unit ID fails
- **THEN** the server refuses the action with an error that is not a denial
