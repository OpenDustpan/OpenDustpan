## ADDED Requirements

### Requirement: The SDK opens a built-in store
The SDK SHALL offer `OpenStore` for a caller that opens a store itself and `WithBuiltinStore` to use it in `NewServer`. The SDK SHALL also offer `WithSQLite` and `WithPostgreSQL` for a server that opens and closes its own store. Each form sets every store port of the server.

#### Scenario: Server with SQLite alone
- **GIVEN** an embedder who calls `store, err := dustpan.OpenStore(ctx, "sqlite:"+path)`, defers `store.Close()`, and calls `dustpan.NewServer(..., dustpan.WithBuiltinStore(store))`
- **WHEN** a client runs one stack update and one workspace state write
- **THEN** both succeed, with no other store option

#### Scenario: Wrong DSN
- **GIVEN** `dustpan.OpenStore` with a DSN that cannot connect
- **WHEN** the embedder calls `OpenStore`
- **THEN** `OpenStore` fails with the error of the store, which names the setting

#### Scenario: Nil store
- **GIVEN** `dustpan.WithBuiltinStore(nil)`
- **WHEN** the embedder calls `NewServer`
- **THEN** `NewServer` fails with an error that names `WithBuiltinStore`

### Requirement: The built-in store adapters are not public
The packages of the built-in SQL store adapters SHALL live under `internal/store/`. Only the store ports and `pkg/store/storetest` stay under `pkg/`.

#### Scenario: Package list
- **WHEN** `go list ./pkg/...` runs
- **THEN** no path holds `store/sqlite`, `store/postgres`, `store/schema` or `store/internal`
