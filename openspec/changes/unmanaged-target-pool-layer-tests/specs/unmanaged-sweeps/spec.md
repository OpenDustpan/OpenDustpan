## ADDED Requirements

### Requirement: Each layer of the target pool check has its own test

The sweep store of each dialect SHALL have one test for the namespace check and one test for the purpose check of `checkTargetPool`, and each test SHALL fail when its check is removed.

#### Scenario: A pool of another namespace
- **WHEN** `checkTargetPool` gets a target row whose pool belongs to another namespace
- **THEN** it returns `unmanaged.ErrInvalidTarget`

#### Scenario: A pool that runs jobs
- **WHEN** a target row written with raw SQL names a pool of the purpose `jobs` and a sweep starts
- **THEN** the start returns `unmanaged.ErrInvalidTarget`
