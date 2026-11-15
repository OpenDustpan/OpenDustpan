## ADDED Requirements

### Requirement: Each append of a server audit entry holds the server scope lock

The PostgreSQL store SHALL hold the lock of the audit scope `@server` for each append of an entry of that scope, also when the caller did not take the lock.

#### Scenario: Parallel writers of server entries
- **WHEN** 16 writers change sign-in provider configs at the same time, and each write appends an entry of `@server`
- **THEN** each write succeeds, and the hash chain of `@server` checks with no break

#### Scenario: An append without a caller lock
- **WHEN** a transaction appends an entry of `@server` and its caller took no lock
- **THEN** a second transaction cannot take the lock of `@server` until the first ends

#### Scenario: The lock fails
- **WHEN** the lock of `@server` returns an error
- **THEN** the transaction rolls back, and neither the entry nor the change of the caller is stored
