# ip-allowlist Specification

## Purpose
TBD - created by archiving change ip-allow-refusal-status. Update Purpose after archive.

## Requirements

### Requirement: A read that the IP allowlist refuses answers 403
The server SHALL answer 403 with the code `access.ip-not-allowed` and the
message `forbidden` for a request that acts on a namespace from outside the
IP allowlist of that namespace, on every route that checks access. The server
SHALL NOT answer the 404 of an unknown object for that refusal, and the answer
SHALL NOT name the list or the namespace.

#### Scenario: A read of the triggers
- **GIVEN** a namespace with an allowlist that does not hold the client address
- **WHEN** the client reads the triggers of a unit of the namespace
- **THEN** the answer is 403 with the code `access.ip-not-allowed`, and no trigger

#### Scenario: A read of the context
- **GIVEN** a namespace with an allowlist that does not hold the client address
- **WHEN** the client reads the context of the namespace or of a unit of it
- **THEN** the answer is 403 with the code `access.ip-not-allowed`, and no context

#### Scenario: A read of the unit settings
- **GIVEN** a namespace with an allowlist that does not hold the client address
- **WHEN** the client reads the settings or the unit defaults of a unit of the namespace
- **THEN** the answer is 403 with the code `access.ip-not-allowed`, and no settings

#### Scenario: An object that the caller cannot read
- **GIVEN** a namespace with no allowlist, or an address that the list holds
- **WHEN** the caller reads an object that it cannot read
- **THEN** the answer is the 404 of an unknown object, unchanged
