## ADDED Requirements

### Requirement: One option opens a built-in store
The SDK SHALL offer one option for each built-in store, `WithSQLite` and `WithPostgreSQL`, that opens the store and sets every store port of the server.

#### Scenario: Server with SQLite alone
- **GIVEN** an embedder who calls `dustpan.NewServer(dustpan.WithSQLite(path))`
- **WHEN** a client runs one stack update and one workspace state write
- **THEN** both succeed, with no other store option

#### Scenario: Wrong DSN
- **GIVEN** `dustpan.WithPostgreSQL` with a DSN that cannot connect
- **WHEN** the embedder calls `NewServer`
- **THEN** `NewServer` fails with the error of the store, which names the setting

### Requirement: The built-in store adapters are not public
The packages of the built-in SQL store adapters SHALL live under `internal/store/`. Only the store ports and `pkg/store/storetest` stay under `pkg/`.

#### Scenario: Package list
- **WHEN** `go list ./pkg/...` runs
- **THEN** no path holds `store/sqlite`, `store/postgres`, `store/schema` or `store/internal`
