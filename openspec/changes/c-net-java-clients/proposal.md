---
version: 2
---

## Why

**Persona: developer.** Developer automates namespaces, units and jobs from .NET and Java services and wants typed clients that match the stable REST API instead of handwritten HTTP.

The user path this change must make work:

1. **Docs page.** Developer reads docs page for automation options
2. **API.** Developer calls tool-neutral REST API with access token from .NET or Java over raw HTTP until clients ship

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 401 Unauthorized: the access token is missing or expired. | Renew the access token and retry the call. | `GET /api/dustpan/v1/capabilities` |
| 403 mfa.fresh-step-required: the call needs a fresh authentication. | Start a new sign-in with a fresh authentication and retry the call. | `GET /api/dustpan/v1/capabilities` |
| 421 Misdirected Request: the namespace is owned by another installation. | Send the request to the primary installation of the namespace. | `GET /api/dustpan/v1/capabilities` |

Evidence for the claims above:

- The roadmap places C# and Java clients after a stable REST API, with Go first then Python and TypeScript. Proof: C# (.NET) and Java clients | the REST API is stable (#1281). Go first, then Python and TypeScript (#966).

## What Changes

- Nothing now: record the deferred C# (.NET) and Java client work and its gates.

Out of scope:

- Stabilizing the REST API
- Go, Python and TypeScript clients
- Terraform CLI support

Depends on:

- none

Builds on (not a change):

- REST API stable (#1281)
- Go client, then Python and TypeScript clients (#966)

## Usability

Where the path still costs the person time or surprises them:

- No user path changes now; .NET and Java users keep using raw HTTP until clients ship.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests client address handling in pkg/dustpan. (`pkg/dustpan/client_address_test.go:188 (TestRequestLogClientIP)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read state. The server may read the contents of every state that it stores, for each tool. Each read is an endpoint with a declared action on a unit (ADR 13, ADR 17)."
- Quote: "Reveal is a separate call. To see a secret value, a client calls reveal with the unit, the version and one value path. The call answers the plain value of that path only. The console shows a masked value until the user selects reveal."
- Effect: Future C# and Java clients read masked state and reveal secrets only through the reveal call. No client work starts until the stability gate passes.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The fence stays. Each write in a namespace carries a `namespace.Fence`, and the store checks it in the same transaction, as ADR 4 decides. The failover version goes up by one at each promotion and at each restore. A manual promotion needs no change to the fence."
- Effect: Future clients send fenced writes. No client work starts until the stability gate passes.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What shape do the C# and Java clients take once the API is stable?
   - Options: Generate both from the API description with the existing client generator; Handwrite thin clients over HTTP.
   - Provisional decision (owner review 14 Nov): Generate both from the API description with the existing client generator when the stability gate passes.
   - Reason: The roadmap gates this work on API stability, so the choice is final only at the gate. Generated clients follow the API description and need no second source of truth. Benchmark: AWS: publish versioned SDKs only against a stable versioned API, with generated code.
   - Persona impact: A Developer who automates from .NET or Java waits for typed clients; raw HTTP still works. Escape hatch: Raw HTTP calls against the stable REST API stay possible.
2. Which toolchain builds the C# and Java clients without a new tool?
   - Options: Extend the existing client generator to emit C# and Java; Use the standard toolchain of each language.
   - Provisional decision (owner review 14 Nov): Extend the existing client generator to emit C# and Java when the stability gate passes.
   - Reason: One generator and one release pipeline add no new tool. Benchmark: AWS: reuse one generator and release pipeline for each SDK language.
   - Persona impact: No Developer path changes now. Escape hatch: Raw HTTP stays possible until the toolchain is decided.

Questions considered and answered:

- Does any accepted ADR already decide the client languages or their order? No. The pack lists no ADR that decides client languages or their order. ADR 41 decides masked reads and reveal, and ADR 248 decision 6 decides the fence on writes.
- Does main already ship a C# or Java client? No. The pack proves only the roadmap row that defers C# and Java until the API is stable and earlier clients land.
- What limits the toolchain choice? A new generator dependency needs the owner. The toolchain choice stays open until the gates pass.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
