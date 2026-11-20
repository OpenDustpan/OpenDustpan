# manual-locks Specification

## Purpose
The manual locks capability covers the locks that a person puts on one unit, one collection or a whole namespace, with a reason that everyone sees. It states what a lock stops (new jobs, queued phases and the lock requests of a tool), what it lets finish (a phase that already runs), and who can remove it. The tool locks of OpenTofu and the update leases of the Pulumi CLI are in the capabilities of those tools, and a force-unlock of a tool never removes a manual lock.

## Requirements

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

### Requirement: bulk-lock

When a caller sends 1 to 100 unit IDs and a reason to the bulk-lock route of a namespace, the server SHALL do the lock of each unit on its own, as a lock call on that unit alone does, and SHALL answer 200 with one result for each ID in the order of the request.

#### Scenario: Mixed result
- **WHEN** one of the units already has a manual lock and the others have none
- **THEN** the answer is 200 with one result for each ID, the result of the locked unit has the error lock.already-locked, the other units are locked with the holder and the reason of the call, and each locked unit has one audit entry

#### Scenario: Too many units
- **WHEN** a call names 101 units, or no unit
- **THEN** the server answers 400 with the error code lock.bulk-size and changes nothing

#### Scenario: A unit that the caller cannot lock
- **WHEN** the caller has unit:read and no unit:lock on one of the units
- **THEN** the result of that unit has the 403 error of a lock call on that unit alone, the server writes a denied audit entry for it, and the other units are locked

#### Scenario: A unit that is not visible
- **WHEN** an ID names a unit that does not exist, a unit in another namespace, or a unit that the caller cannot read
- **THEN** the three results are equal: 404 with the error code lock.target-not-found

#### Scenario: Retry with the same key
- **WHEN** the caller sends the call again with the same Idempotency-Key and the same body
- **THEN** each unit that the first call locked answers its stored result and is not locked a second time, and each unit that failed with 5xx or 429 is done again

### Requirement: bulk-unlock

When a caller sends 1 to 100 unit IDs to the bulk-unlock route of a namespace, the server SHALL remove the lock of each unit that the caller holds, as an unlock call on that unit alone does, and SHALL answer 200 with one result for each ID in the order of the request.

#### Scenario: Lock of another holder
- **WHEN** another person holds the lock of one of the units
- **THEN** the result of that unit has the error lock.not-holder and the lock stays, and the locks that the caller holds are removed

#### Scenario: Unit with no lock
- **WHEN** a unit has no manual lock
- **THEN** the result of that unit has the error lock.not-locked and the other units keep their results

#### Scenario: Repeated ID
- **WHEN** one ID is in the call two times and the caller holds the lock
- **THEN** the first result is ok and the second result has the error lock.not-locked
