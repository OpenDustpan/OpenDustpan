# 355. Gate configuration paths and errors as shipped

Status: accepted · 2026-11-07. Accepted by the project owner on 7 November 2026 (records the shipped behavior of ADR 230 decisions 3 and 4).

Changes:

- Changes ADR 230 decision 3: the unit path of the gate routes has the segment `by-name`.
- Changes ADR 230 decision 4: the error table has two more rows, `gate.ErrNotFound` and `gate.ErrConflict`.

## Context

ADR 230 decision 3 gives the unit path of the gate routes as `.../units/{tool}/{name}`. The server and the reference document use `.../units/by-name/{tool}/{name}`. The segment `by-name` keeps a path by name apart from the routes of a unit by its stable ID.

ADR 230 decision 4 has no row for two sentinels that the handler maps to a status and a code.

Evidence, checked on main at commit `3d716d3b11ff66821042a655cfc93a360eb39563`:

- The unit path: `internal/api/server/gate_objects.go:23` sets `gateUnitBase` to `/api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}`.
- The reference document: `docs/reference/gate-config-api.md:19` gives the same path.
- The not-found mapping: `internal/api/server/gate_config.go:631` maps `gate.ErrNotFound` to status 404 and the code `gate.not-found`. The code value is at `internal/errcode/catalog_gate.go:26`. The sentinel is at `pkg/gate/gate.go:68`.
- The version-conflict mapping: `internal/api/server/gate_config.go:633` maps `gate.ErrConflict` to status 409 and the code `gate.version-conflict`. The code value is at `internal/errcode/catalog_gate.go:31`. The sentinel is at `pkg/gate/gate.go:76`.

## Decision

1. The unit scope of the gate routes is `/api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}`. The segment `by-name` is part of the path. Each variable segment is percent-encoded. For example, the Pulumi stack `web/prod` of the namespace `acme` is `.../namespaces/acme/units/by-name/pulumi/web%2Fprod`. The other scopes of ADR 230 decision 3 do not change.

2. A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`. The object does not exist at the scope. A gate configuration error that wraps `gate.ErrConflict` has the status 409 and the code `gate.version-conflict`. The stored version is not the version of `If-Match`. This decision adds two rows to the error table of ADR 230 decision 4. The other rows do not change.

## Consequences

- A reader of ADR 230 decision 3 uses the unit path of decision 1 of this ADR.
- A reader of ADR 230 decision 4 finds the two codes of decision 2 of this ADR.
- This ADR changes no code and no API. The code and the reference document already follow it.
