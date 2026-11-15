## ADDED Requirements

### Requirement: A new job acts only on a unit of its fence namespace

When the job store creates a job, it SHALL read the namespace of the live unit with the unit ID of the job in the same transaction, and SHALL refuse the job and write nothing when that namespace is not the namespace of the fence or when no live unit has that ID.

#### Scenario: A unit ID of another namespace
- **WHEN** a caller creates a job with the fence of namespace `acme` and a unit reference that names `acme` with the unit ID of a unit in namespace `beta`
- **THEN** the store refuses the job as invalid, and no job, operation or audit entry is written in either namespace

#### Scenario: An unknown unit ID
- **WHEN** a caller creates a job with a unit ID that names no live unit
- **THEN** the store refuses the job as invalid and writes nothing
