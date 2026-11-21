## Context

The rule of job tokens (`pkg/identity/jobtoken_rule.go`) compares stable unit IDs. The routes by name build an object with no stable ID. The access authorizer reads the ID for every other principal. This change moves the same read in front of the comparison for a job token.

## Contract

```go
// UnitIDReader returns the stable ID of the unit obj, and false when no such unit exists
// or obj is not a unit. It has the signature of access.Units.UnitID.
type UnitIDReader interface {
	UnitID(ctx context.Context, obj Object) (string, bool, error)
}

type JobTokenRule struct {
	Next    Authorizer
	Units   JobUnits
	UnitIDs UnitIDReader // NEW. Nil refuses a unit object with no stable ID.
}
```

- An object that is a unit and has a stable ID is not read again.
- A unit not found: the rule answers a denial (`DeniedError`), as for another unit.
- A port error: the rule answers an error that wraps the cause and is not a denial (fail closed, as for `UnitOfJob`).
- No new route and no new error code. Round trips: one read for each request of a job token on a route by name; other principals are not changed.
