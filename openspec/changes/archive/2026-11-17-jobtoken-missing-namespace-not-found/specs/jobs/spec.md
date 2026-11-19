## ADDED Requirements

### Requirement: A job token issue into a namespace with no row is not found

When a job token store issues a token with a fence whose namespace has no row, it SHALL return `namespace.ErrNotFound` and write nothing, in every implementation of the port.

#### Scenario: The namespace was deleted
- **WHEN** a caller issues a job token with the fence of a namespace that was deleted
- **THEN** the store returns namespace.ErrNotFound, not namespace.ErrFenced, and stores no token
