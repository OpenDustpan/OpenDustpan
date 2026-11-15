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
