## ADDED Requirements

### Requirement: sqlite-wiring-single-point

When the server starts from cmd/opendustpan with a SQLite store, the server SHALL open its built-in store through dustpan.WithSQLite.

#### Scenario: Server command uses SQLite constructor
- **WHEN** the server starts from cmd/opendustpan with a SQLite store
- **THEN** the server opens the store through dustpan.WithSQLite with the configured path

### Requirement: postgres-wiring-single-point

When the server starts from cmd/opendustpan with a PostgreSQL store, the server SHALL open its built-in store through dustpan.WithPostgreSQL.

#### Scenario: Server command uses PostgreSQL constructor
- **WHEN** the server starts from cmd/opendustpan with a PostgreSQL store
- **THEN** the server opens the store through dustpan.WithPostgreSQL with the configured DSN
