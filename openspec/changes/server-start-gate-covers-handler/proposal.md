---
version: 1
---

Issue: #1944.

## Problem

An embedder builds dustpan.Server and calls Server.Workspaces, Server.Backups and Server.Restores. Handler answers 503 until Start succeeds, but the direct accessors return services with no check. Early use races the fencing of stopped backup and restore rows and can write state before the key check refuses a wrong key-encryption key.

## Evidence

- withStartGate is used only for Handler. Proof: rg for withStartGate gives pkg/dustpan/dustpan.go:1029 and pkg/dustpan/start.go:89.
- The three accessors exist at fixed lines with no gate named in the pack. Proof: main lists pkg/dustpan/dustpan.go:1963, :1969 and :1975.
- Handler wiring uses withStartGate with the start state. Proof: pkg/dustpan/dustpan.go:1029 handler: withStartGate(handlers.served, started, cfg.logger),

## Fix boundary

- Server.Workspaces, Server.Backups and Server.Restores refuse use with the NEW error ErrNotStarted until Start has succeeded; the RFC fixes the return shape.
- Each accessor doc states the Start-first rule; internal Start and Run paths use ungated fields.

Out of scope:

- New backup or restore behavior after Start
- Change to the words of ADR 238 decisions 14 and 15

## Check

- Write the RFC that extends the conformance list of ADR 238 decision 15 and fixes the gate shape of Server.Workspaces, Server.Backups and Server.Restores in NEW file docs/rfc/start-gate-accessors.md. Check: Owner accepts the RFC in NEW file docs/rfc/start-gate-accessors.md that extends the conformance list of ADR 238 decision 15; exercises ANY /api/server/backups with access token.
- Write failing test TestServerAccessorsRefuseBeforeStart in NEW file pkg/dustpan/start_gate_test.go for calls before Start, after failed Start, after success, and during Start. Check: The NEW test TestServerAccessorsRefuseBeforeStart in NEW file pkg/dustpan/start_gate_test.go fails on main; exercises ANY /api/server/backups with access token through the start gate.
- Refine this change when its RFC is accepted. Confirmed by: the driver's gates. Check: BLOCKED by the RFC gate-shape decision; after the RFC, exercises ANY /api/server/backups with access token and lists wiring, docs and usability check.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Server.Handler returns the gated handler. (`pkg/dustpan/dustpan.go:1958 (func Server.Handler)`)
- Server.Workspaces returns the service with no start check. (`pkg/dustpan/dustpan.go:1963 (func Server.Workspaces)`)
- Server.Backups returns the service with no start check. (`pkg/dustpan/dustpan.go:1969 (func Server.Backups)`)
- Server.Restores returns the service with no start check. (`pkg/dustpan/dustpan.go:1975 (func Server.Restores)`)
- withStartGate answers 503 until Start succeeded. (`pkg/dustpan/start.go:89 (func withStartGate)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 238: `opendustpan serve` needs decisions: an explicit key and database, `key generate` and `doctor`** (not owner-tagged).

- Decisions that apply: 14 (current), 15 (current).
- Changed by: ADR 315 extends; ADR 356 changes decision 3. Chain to the end: ADR 315, ADR 356.
- Current rule: this ADR, with ADR 315, 356.
- Quote: "`Handler` answers 503 until `Start` succeeded. Each route answers 503 until then, except `GET /health/live`, which stays 204 (ADR 34)."
- Quote: "`Server.Start` and a gated `Handler`. A new exported method, `Server.Start(ctx) error`, runs the start-up steps: the key check, the claim of the installation kind (ADR 237 decision 26), the running backups and restores with the fence, the audit chain and the sign-in providers. `Run` calls `Start`. `Start` runs"
- Effect: Extends the fail-closed rule from Handler to Server.Workspaces, Server.Backups and Server.Restores. An RFC extends the conformance list of decision 15 first.
- Conflict: rfc_needed.

## Open questions

1. Which gate shape ships for the SDK: accessors return (service, error), or each service method refuses each call until Start? Decides: owner.
   - Options: Accessors return (service, error) with NEW ErrNotStarted; Each service method refuses with NEW ErrNotStarted until Start succeeds.
   - Recommended: Accessors return (service, error) with NEW ErrNotStarted. Reason: Refusal at the closest boundary leaves service methods unchanged. A signature break is allowed before 1.0.

Not spec-ready: the change conflicts with ADR 238: an RFC comes first.

Questions considered and answered:

- Must direct service use wait for Start? ADR 238 decisions 14 and 15 require a forgotten Start to fail closed for HTTP. The three accessors have no such rule, so an RFC decides the gate shape before code.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
