## ADDED Requirements

### Requirement: Retry after a failed seed write

When a seed write fails, the harness SHALL discard the failure so that the next seed call for that server writes again.

#### Scenario: Retry after recovery
- **WHEN** a seed write fails and the server then recovers
- **THEN** the next seed call writes the seed profile and succeeds

### Requirement: Seed state per server instance

When a server instance closes, the harness SHALL delete the seed state of that instance.

#### Scenario: Reused address reseeds
- **WHEN** a new server gets the address of a closed server
- **THEN** the harness posts the seed profile to the new server

#### Scenario: Close during a write
- **WHEN** a server closes while a seed write is in flight
- **THEN** a late success stores no entry for the reused address

#### Scenario: Canceled caller
- **WHEN** the context of a seed call is canceled
- **THEN** no failure entry is stored and the next call writes again

### Requirement: Seed once per live instance

While a server instance stays open, the harness SHALL post the seed profile at most once.

#### Scenario: Single post per instance
- **WHEN** the seed write succeeded for a live server instance
- **THEN** later calls reuse the cached success without a new post
