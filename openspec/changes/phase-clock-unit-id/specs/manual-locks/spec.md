## ADDED Requirements

### Requirement: A running job passes a manual lock after a rename
The server SHALL compare the stable unit ID and the namespace ID, not the unit
name, when a job token asks whether its phase started before a manual lock. A
rename of the unit SHALL NOT change the answer.

#### Scenario: Rename the unit of a running job
- **GIVEN** a running job with a live phase on unit `shop/prod` in namespace `shop`
- **WHEN** the unit is renamed to `shop/live` and a person takes a manual lock on `shop/live`
- **THEN** the lock request of the job passes, because its phase started before the lock

#### Scenario: Another unit with the same name
- **GIVEN** a running job on unit `shop/prod` with ID A
- **WHEN** a lock request names another unit with the name `shop/prod` and ID B
- **THEN** the phase clock answers `ErrNoPhase` and the request does not pass the lock
