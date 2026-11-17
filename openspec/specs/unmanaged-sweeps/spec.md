# unmanaged-sweeps Specification

## Purpose
An unmanaged sweep finds resources that no unit manages, on the pool of a stored target. This capability covers the check that the pool of the target belongs to the namespace of the sweep. It does not cover the sweep findings or the actions on them.

## Requirements

### Requirement: Each layer of the target pool check has its own test

The sweep store of each dialect SHALL have one test for the namespace check and one test for the purpose check of `checkTargetPool`, and each test SHALL fail when its check is removed.

#### Scenario: A pool of another namespace
- **WHEN** `checkTargetPool` gets a target row whose pool belongs to another namespace
- **THEN** it returns `unmanaged.ErrInvalidTarget`

#### Scenario: A pool that runs jobs
- **WHEN** a target row written with raw SQL names a pool of the purpose `jobs` and a sweep starts
- **THEN** the start returns `unmanaged.ErrInvalidTarget`
