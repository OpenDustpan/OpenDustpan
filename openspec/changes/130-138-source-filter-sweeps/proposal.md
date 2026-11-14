---
version: 1
---

## Why

**Persona: namespace administrator.** A namespace administrator restricts which code sources a namespace may use and which cloud scopes a sweep may read. A job or a sweep can still start outside these limits, and an oversize or out-of-scope report can waste the store. This change refuses those creates and claims and bounds the report stream.

The user path this change must make work:

1. **API.** Set the source filter with PUT /api/dustpan/v1/namespaces/{namespace}/source-filter, using an access token.
2. **API.** A runner claims a sweep through the runner protocol under /runner/v1, using its runner key.
3. **Docs page.** Follow the NEW guide docs/guides/runners.md to set the filter, pick an operator sweep pool and read the report bounds.
4. **E2e.** Set a filter with an access token, create a job outside the filter, and expect the refusal.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job create names a code source outside the source filter. | The server answers 403 `source.filter-invalid`: the source is not allowed. Read the filter. | `GET /api/dustpan/v1/namespaces/{namespace}/source-filter` |
| A sweep target names a namespace pool, or a pool without the sweep purpose. | The server answers 409 `runner.invalid`: a sweep runs only on an operator pool with the sweep purpose. Read the pools. | `GET /api/runners/{namespace}/pools` |
| A report header lists a scope or a region outside the target. | The server answers 422 `runner.invalid`: the scope is not in the target. The sweep ends failed and closes no finding. | `GET /api/runners/{namespace}/pools` |
| A report is over a bound. | The server answers 413 `runner.limit`: the report is over the bound. The sweep ends failed. Narrow the regions and start again. | `GET /api/runners/{namespace}/pools` |

Evidence for the claims above:

- The source filter route exists on main. Proof: ANY/GET/PUT /api/dustpan/v1/namespaces/{}/source-filter in the route table
- The source filter error codes exist. Proof: internal/errcode/catalog_source.go:8 source.filter-invalid
- The sweep claim type and the report header type exist. Proof: pkg/runner/store.go:269 SweepClaim and internal/unmanaged/report.go:43 ReportHeader
- The runner pool route exists on main. Proof: ANY /api/runners/{}/pools in the route table
- The guide does not exist yet. Proof: The pack path list shows docs/guides/runners.md with exists false.

## What Changes

- Check the stored source.Filter when a job names a code source.
- Refuse a sweep target whose pool is not an operator pool with the sweep purpose.
- Refuse a report header scope or region outside the target, a wrong format and an unknown version.
- Enforce the report bounds, each with its own test.
- Fail a truncated or oversize sweep: it never reaches matching and closes no finding.
- Add the NEW guide docs/guides/runners.md.

Out of scope:

- Runner pool create, update, delete and join tokens (130-138-runner-pools-management)
- Runner poll, heartbeat and claim renewal (130-138-runner-protocol-claims)
- Launcher step start (130-138-launcher-steps-execution)
- Sweep targets lifecycle, findings triage and the schedule worker (160-165 items)

Depends on:

- 130-138-runner-pools-management
- 130-138-runner-protocol-claims
- `130-138-launcher-steps-execution` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The create error for a blocked source names the filter route, but not which pattern blocked it. The audit entry names it.
- A runner operator learns the report bounds only from the guide, so the guide lists each bound.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The store lists source filters. (`pkg/store/postgres/sourcefilter.go:21 (func Store.SourceFilters)`)
- The server wires source filters through an option. (`pkg/dustpan/sourcefilter.go:15 (func WithSourceFilters)`)
- The runner package defines the sweep claim type. (`pkg/runner/store.go:269 (type SweepClaim)`)
- The runner package defines the sweep claimer port. (`pkg/runner/store.go:244 (type SweepClaimer)`)
- The unmanaged package defines the report header. (`internal/unmanaged/report.go:43 (type ReportHeader)`)
- The source filter error codes exist. (`internal/errcode/catalog_source.go:8 (code source.filter-invalid)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 11 (current), 13 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "Bounds. A report is at most 1 GiB and 5,000,000 records. A line is at most 64 KiB. A record has at most 50 tags, with a key of at most 128 bytes and a value of at most 256 bytes. A header has at most 1,000 scopes."
- Quote: "- **The header is a subset of the target.** Each scope of the header must be a scope of the target, with only regions of the target when the target lists regions."
- Effect: Enforces the bounds and the header subset at report intake.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches, quota reservations and quota counts, the source filter, safe delete"
- Effect: The source filter stays a fixed control. The service checks it and no gate is added.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 9).

- Decisions that apply: 9 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "Source filter (owner). The setting of ADR 135 decision 6 is now the **source filter**: `source.Filter`, at the route `.../namespaces/{namespace}/source-filter`. It stays a fixed control."
- Effect: The stored source.Filter is the enforced value.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: A sweep runs only on a runner.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: evidence path `docs/guides/runners.md` does not exist in the checkout.

Questions considered and answered:

- Does a job create need a new gate for the source check? No. ADR 222 decision 4 keeps the source filter a fixed control, so the service checks it.
- Which pool may run a sweep? Owner decision, 13 Nov 2026 (queue P14): only an operator pool with the sweep purpose. A namespace pool with the sweep purpose is refused, fail closed.
- Does the source filter apply to a sweep? No. A sweep has a command and no code source. ADR 164 decisions 8 and 13 give its checks.
- What happens when the filter or target is narrowed after create? The filter is checked at create and at the claim of a job. A narrowed target fails the header check.
- What is the end state of a truncated or oversize report? The sweep ends failed. ADR 164 decision 13 says what a sweep may close, and a failed sweep closes nothing.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
