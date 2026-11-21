# job-token-authorization Specification

## Purpose
A job token is a credential that the server gives to the runner of one job. This capability covers what a job token may do: it acts only on the unit of its job, and the output read that an output-sharing rule allows. It does not cover how the token is issued or how a runner stores it.

## Requirements

### Requirement: A job token acts on its own unit

The server SHALL allow a job token to act only on the unit of its job, by the stable unit ID of the job, except the output read that an output-sharing rule allows.

#### Scenario: Another unit
- **WHEN** a job token asks for an action on a unit that is not the unit of its job
- **THEN** the server refuses with identity.ErrDenied and writes an audit entry

#### Scenario: No elevation
- **WHEN** a job token principal carries an administrator mark
- **THEN** the rule clears the mark and the token gets no more than its unit

### Requirement: An inconsistent job token is refused

The server SHALL refuse every action of a principal that carries a job ID and is not a consistent job token.

#### Scenario: Job ID without a job token
- **WHEN** a principal with a job ID but no job token credential asks for any action
- **THEN** the server refuses with identity.ErrDenied

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
