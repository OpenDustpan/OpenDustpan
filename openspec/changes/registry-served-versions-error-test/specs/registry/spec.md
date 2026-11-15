## ADDED Requirements

### Requirement: A read error of the served versions reaches the caller

When the server cannot read the servable releases of a provider, the list of its versions and the read of one version SHALL answer the error and no partial data.

#### Scenario: The read fails
- **WHEN** the read of the servable releases fails with an error other than not found
- **THEN** the route answers 500 with the generic error text and no list or release

### Requirement: The reference names what a replayed publish answers

The API reference SHALL say that a publish replayed with the same idempotency key answers the release as it was at the time of the first publish.

#### Scenario: A provider author reads the reference
- **WHEN** a provider author reads the section on publish
- **THEN** the page says that a replayed answer describes the release at the time of the first publish
