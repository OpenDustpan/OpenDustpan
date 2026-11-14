---
version: 1
---

Issue: #969, #995, #1001, #1004.

## Problem

A namespace administrator moves stacks and workspaces into OpenDustpan from a TFE-compatible API and needs the first import source adapter to read each workspace and each state version.

## Evidence

- The source conformance suite exists on main. Proof: internal/unitimport/unitimporttest/source.go:35 (func RunSource)
- The import hold errors exist on main. Proof: internal/unitimport/unitimport.go:59 (ErrHoldRemoved)
- No tool suite runner exists on main. Proof: rg -n -F --glob '!*_test.go' 'unitimporttest.RunTool' pkg internal cmd gives 0 matches in the pack

## Fix boundary

- NEW tfe-api source adapter with Units, Versions, Open and Locker capability
- Source conformance run for tfe-api
- E2e step with OpenDustpan as the TFE-compatible source
- Update docs/guides/import.md for the tfe-api kind

Out of scope:

- Import routes, engine, durable work and release
- pulumi-service, s3 and opentofu-http adapters
- Tool port adapters in stack and workspace
- Quota reservations
- Move check

## Check

- Add NEW internal/unitimport/tfeapi/source_test.go with TestTfeApiSourceConformance that runs RunSource with a seeded fixture and a bad credential. Check: TestTfeApiSourceConformance fails on main. No route: the test drives the adapter against a local TFE fake only.
- Add NEW internal/unitimport/tfeapi/hostile_test.go for private address, cross-host redirect, oversize body, timeout and 5xx. Check: Hostile tests fail on main. No route: the test drives the adapter against a local TFE fake only.
- Write NEW internal/unitimport/tfeapi/source.go with the NEW credential type and the Locker capability. Check: Both test files pass. No route: the test drives the adapter only.
- Register the NEW error codes import.source-denied, import.source-not-found and import.source-unavailable in the NEW file internal/errcode/catalog_import.go. Check: The catalog test lists the three codes. No route: the test reads the catalog file only.
- Add the e2e step in NEW e2e/import_tfe_test.go with server S as TFE-compatible source and server D as target. Check: The step passes with the real CLIs on SQLite and PostgreSQL; it calls `ANY /api/v2/state-versions/{}/resources` on server S with an access token and passes its read gate. Confirmed by: CI (e2e).
- Update docs/guides/import.md for the tfe-api kind. Check: The page names tfe-api and the tested source only. No route: the test reads the guide file only.
- Run conformance, e2e, docs lint and the full check. Check: make check reports zero findings. No route: the test runs static checks only. Confirmed by: CI (make check).

Depends on:

- #1001

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The source conformance entry runs the source suite. (`internal/unitimport/unitimporttest/source.go:35 (func RunSource)`)
- ErrHoldRemoved reports a write to a target unit that the import hold no longer covers. (`internal/unitimport/unitimport.go:59`)
- ErrTargetDeleted reports a write to a deleted target unit. (`internal/unitimport/unitimport.go:56`)
- The e2e suite drives a real OpenTofu binary. (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 241: An import is an API resource that the server runs** (not owner-tagged).

- Decisions that apply: 4 (current), 6 (current).
- Changed by: ADR 248 changes decision 11; ADR 313 extends; ADR 343 changes decisions 2 and 3. Chain to the end: ADR 248, ADR 313, ADR 343, ADR 286.
- Current rule: this ADR, with ADR 248, 313, 343.
- Quote: "An import never stores a source credential (product manager)."
- Quote: "The egress rules check each source address. Each source adapter that the server runs sends its requests through `egresshttp`."
- Effect: The adapter never stores the source credential and sends each request through egresshttp.
- Conflict: none.

**ADR 242: What an import moves: history, sealing and secrets** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "What moves from a TFE-compatible API."
- Effect: The adapter reads the state file, serial, lineage and MD5 of each state version verbatim.
- Conflict: none.

**ADR 243: The source port, its adapters and their conformance suites** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current), 8 (current), 9 (current), 11 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The order of the first adapters (product manager)."
- Quote: "One suite for each port, first. `pkg/unitimport/unitimporttest` holds three suites."
- Effect: The change adds the first adapter of the ordered list, runs its port suite first and names the protocol tfe-api.
- Conflict: none.

**ADR 244: The move check, the guide pages and the e2e steps** (not owner-tagged).

- Decisions that apply: 14 (current), 15 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The e2e steps (ADR 12)."
- Quote: "What the docs say about support."
- Effect: The adapter lands with its e2e step first and the docs name only what the suite drove.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which source kind lands first? ADR 243 decision 8 gives tfe-api first and decision 9 fixes the protocol name.
- Does this change add routes or durable work? No. The routes and the engine belong to a separate item.
- Does the adapter store credentials or map source users? No. ADR 241 decision 4 forbids storage and ADR 244 decision 16 reports users as text only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
