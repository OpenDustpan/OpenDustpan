## ADDED Requirements

### Requirement: One lock order for the server audit scope

The PostgreSQL store SHALL take the lock of the audit scope `@server` as the first statement of each transaction that appends an entry of that scope, before it writes any other row, and SHALL refuse an append of an entry of that scope outside such a transaction.

#### Scenario: A passkey use with a regression and the removal of the same passkey
- **WHEN** a passkey use that finds a counter regression and the removal of the same passkey run at the same time, 50 times
- **THEN** each call ends with its own result, and no call fails with SQLSTATE 40P01

#### Scenario: Parallel writers of server entries
- **WHEN** 16 writers change sign-in provider configs at the same time, and each write appends an entry of `@server`
- **THEN** each write succeeds, and the hash chain of `@server` checks with no break

#### Scenario: An append outside a locked transaction
- **WHEN** a store function appends an entry of `@server` in a transaction that did not take the lock first
- **THEN** the append is refused with `audit.ErrInvalidEntry`, and nothing is stored

#### Scenario: A passkey use with no regression
- **WHEN** a person signs in with a passkey and the counter goes up
- **THEN** the store updates the counter without the `@server` lock
