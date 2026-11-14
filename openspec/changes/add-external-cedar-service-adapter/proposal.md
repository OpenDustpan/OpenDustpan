---
version: 8
---

Issue: #1002.

## Why

**Persona: operator.** An Operator installs and configures the server and wants to name an external Cedar service as the gate engine, so policy evaluation runs in one approved service while access limits stay on the linked adapter. The request and answer format, the save fallback and the access-limit split during an outage are decided by ADR 377 decisions 1 to 3.

The user path this change must make work:

1. **Server start.** Operator sets the external Cedar service address, CA and credential from docs/reference/ and restarts the server.
2. **API.** Namespace administrator saves a gate through the API; the server parses and type-checks it with the linked validator.
3. **CLI.** Developer runs the Pulumi CLI or OpenTofu; the server evaluates gates through the service.
4. **Docs page.** Operator reads the configuration page in docs/reference/ for the service address and the credential.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Gate save with a bad condition or unknown fact | Gate save refused: gate invalid against fact schema; fix the gate document and retry the PUT of the gate. | `PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |
| External service answer fails the echo check | Gate evaluation unavailable: the external Cedar service did not authenticate or did not echo both IDs; check the service address, CA and credential in the installation settings. | `GET /api/dustpan/v1/settings` |
| Service address, CA or credential fails the write check | Service settings refused: endpoint is not https, or CA or credential is missing; fix the values and write the settings again. | `PUT /api/dustpan/v1/settings` |

Evidence for the claims above:

- The engine port lives in pkg/gate/engine.go. Proof: pkg/gate/engine.go
- The conformance runner exists on main. Proof: pkg/gate/gatetest/run_engine.go:29 (func RunEngine)
- Issue 1002 keeps external checks and starter packs out of scope. Proof: Out: External checks (RFC 49) and starter packs.

## What Changes

- Service adapter of gate.Engine with TLS, credential, ID echo and version check
- Configuration for the external Cedar service address, CA and credential
- Docs page in docs/reference/ for the configuration
- Save check operation and CI validator job with the official Rust cedar CLI image, as ADR 377 decisions 1 and 2 say

Out of scope:

- External checks of RFC 49 and starter packs
- Linked cedar-go adapter of #996
- New decision points or new gate fields

Depends on:

- add-access-limits-per-scope

Builds on (not a change):

- #996

## Usability

Where the path still costs the person time or surprises them:

- Operator sets address, CA and credential from one docs page.
- An outage can surprise during plan and apply; the record names the miss.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- gate.ErrUnavailable is the unavailable sentinel. (`pkg/gate/gate.go:76`)
- RunEngine runs each engine case against a factory. (`pkg/gate/gatetest/run_engine.go:29`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 7).

- Decisions that apply: 7 (extended).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Cedar, linked and as a service (owner). The server links `cedar-go` and can also call an external Cedar service, behind one deep engine port (ADR 224 decision 9)."
- Effect: The change adds the service adapter behind the same port. Linked cedar-go stays the default.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 9, 13).

- Decisions that apply: 8 (current), 9 (extended), 13 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Check at save (PM). The server parses and type-checks each gate at save against the fact schema of its decision point, with the schema validator of `cedar-go`."
- Quote: "One deep engine port, two adapters (owner). The port `gate.Engine` takes the compiled policy set of a decision point, the entities and one request for each unit."
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`."
- Effect: The adapter is the second adapter. Save keeps the local parse and type check. The service check at save is added only as the owner decides. Protocol faults give ErrUnavailable.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The gate engine port stays pluggable (owner, 2026-11-07). The port `gate.Engine` of ADR 224 decision 9 stays the one place where an engine plugs in."
- Effect: The service is an adapter of gate.Engine and runs the same conformance suite.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which HTTP protocol does the external service speak? A small, versioned JSON format over HTTPS that OpenDustpan defines. ADR 377 decision 1: "The external adapter speaks a small, versioned JSON format over HTTPS that OpenDustpan defines. The version is part of the path." The docs in docs/reference describe it.
- What are the evaluate operation and the check operation? Two paths, `evaluate` and `check`. ADR 377 decision 1: "The format has two paths: `evaluate` ... `check` ..."
- Which container image runs the Rust validator in CI? The pinned image of the official Rust cedar CLI. ADR 377 decision 2: "CI runs the `validate` command of the official Rust `cedar` CLI ... in an image that CI pins by digest."
- What is the save fallback when the service does not answer the check operation? ADR 377 decision 3: the outage rules stay as ADR 224 decision 8 says: "When the service does not answer, a save that switches a gate to `evaluate`, removes it or sets `onError: skip` goes on with the linked validator only, and the audit entry says so. Every other save fails with `gate.ErrUnavailable`."
- Do access limits stay on the linked adapter during a service outage? Yes. ADR 377 decision 3, quoting ADR 224 decision 9: "Access limits (ADR 223) always use the linked adapter, so an outage of the service never refuses every API call."
- Is an external Cedar service allowed as a gate engine? Yes. ADR 224 decision 9, ADR 222 decision 7 and ADR 335 decision 7 allow it.
- Must a gate save still check the gate locally? Yes. ADR 224 decision 8 requires the cedar-go parse and type check, so the save does not depend on the service for that check.
- Does every engine fault give the same error? No. Protocol faults give ErrUnavailable. A Cedar diagnostic that passes intact follows ADR 224 decision 13.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
