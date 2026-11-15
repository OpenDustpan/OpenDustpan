## Context

Short-form security bug. The job store trusts the unit ID of the unit
reference of a new job. The fix reads the unit row by ID inside the write
transaction.

## Contract

- NEW method of `jobstore.Tx`: `UnitNamespace(ctx context.Context, unitID string) (namespace.Name, bool, error)`. It returns the namespace of the live unit with the ID, and false when no live unit has the ID. It reads in the transaction of the write.
- NEW sqlc query `GetUnitNamespaceByID :one` in `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql`, in name order next to `GetInventoryEntryByUnitID`: `SELECT namespace FROM inventory WHERE unit_id = ?` (PostgreSQL `$1`).
- `jobstore.Store.Create`: returns `job.ErrInvalid` and writes nothing when the unit ID names a live unit of another namespace than the fence, or no live unit.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| store (shared job store, both dialects) | `createIn` reads the namespace of the unit by ID in the write transaction and compares it with the fence. | `testUnitIDOfOtherNamespace` in the job suite, run by SQLite and PostgreSQL |
| memory double | `memDB.admit` compares the namespace of the unit in `units` with the fence. | the same suite case, run by the memory double |
| service | `Service.Start` passes the refusal on and starts no job. | `TestStartRefusesUnitIDOfOtherNamespace` |
| HTTP handler | The create route resolves the unit from the namespace of the path and the tool and name of the body; it never takes a unit ID from the client. | `TestCreateJobNeverTakesAUnitID` |
| fail closed | An unknown unit ID, or an error of the read, refuses the job. | the suite case, with an unknown ID |
