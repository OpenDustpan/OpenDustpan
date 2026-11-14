---
version: 2
---

Roadmap: `docs/roadmap.md:402` (Console).

## Why

**Persona: developer.** Developer wants to make a unit from a published blueprint without reading the raw API. Today the catalog and create form have no proven path, so the developer cannot see blueprints or learn which input is wrong.

The user path this change must make work:

1. **Console.** Open the catalog of the namespace and pick a blueprint
2. **API.** Open one blueprint version and read its ordered input schema through the REST API of the blueprints change
3. **API.** Fill inputs and send the create request with an Idempotency-Key through the REST API of the blueprints change
4. **Console.** Read the error that names the bad input, fix it, and make the unit
5. **Docs page.** Follow the docs page for the catalog create flow
6. **E2e.** Run the browser suite against the real binary over TLS

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create form sends an input that the blueprint does not allow | The form shows each message unchanged as plain text next to that input. Fix the inputs in the form, then send again. Re-read the blueprint with `GET /api/dustpan/v1/self-service/blueprints/{}`. | `GET /api/dustpan/v1/self-service/blueprints/{}` |
| Catalog of a namespace holds no published blueprint | The catalog shows an empty state with a link back to the namespace home. Publish a blueprint first, then open the catalog again. Check one blueprint with `GET /api/dustpan/v1/self-service/blueprints/{}`. | `GET /api/dustpan/v1/self-service/blueprints/{}` |
| Catalog opens a blueprint that no longer exists | The catalog shows a not-found message and a link back to the catalog. Pick another blueprint. Check a version with `GET /api/dustpan/v1/self-service/blueprints/{}/{}/versions/{}`. | `GET /api/dustpan/v1/self-service/blueprints/{}/{}/versions/{}` |
| Catalog list or create request fails with a server outage | The console shows a retry message with the request ID. Wait, then open the catalog again. Check server readiness with `GET /health/ready`. | `GET /health/ready` |

Evidence for the claims above:

- A guide for publishing a blueprint exists. Proof: docs/guides/publish-a-blueprint.md
- The milestone step needs browser tests that open the catalog, fill the create form, see the error of a bad input, and make a unit. Proof: The browser tests of the console repository run against the server of the suite. They open the catalog, fill the create form of a blueprint, see the error of a bad input, and make a unit.
- No e2e file in this pack covers Blueprint. Proof: Blueprint e2e: [] with e2e_count 0

## What Changes

- Console catalog page lists published blueprints of a namespace through the routes of the blueprints change. Those blueprint routes are new in that change, not in this one.
- Console create form shows the versioned input schema in server order and sends a unit request with the blueprint stable ID
- Console create form shows each input error unchanged as plain text next to the named input with raw JSON mode
- Browser tests prove open catalog, bad input error, idempotent retry, and make of a unit

Out of scope:

- Blueprint publish and edit flows
- Blueprint route, input check, quota, unit write, grant model, and audit record owned by blueprints change
- Ephemeral units
- Environment promotion
- Artifact registry and curated upstreams
- Upgrade pull requests
- Job proxy

Depends on:

- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The form still costs time when blueprint input schemas are long: the developer scrolls past many inputs even with stable order and version filtering.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Console control paths have a test (`pkg/dustpan/console_control_test.go:14 (TestConsoleControlPaths)`)
- Blueprint read refuses hostile rows in the postgres store (`pkg/store/postgres/self_service_blueprints_internal_test.go:93 (TestSelfServiceBlueprintReadRefusesHostileRows)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which ADR decides the catalog list and create request shape? The pack lists adrs as []. The roadmap cites RFC 14 as accepted with domain core built but gives no decision text to quote. The blueprints change owns the catalog routes, the input check, the unit write, the grant model, and the audit record, so no ADR decision applies in this change.
- Which API does the catalog create form use to list blueprints and request a unit? Only the tool-neutral REST API that the blueprints change owns. This change sends the blueprint stable ID and the Idempotency-Key through it. The browser suite proves the console path against the real server.
- Shall the create form show the server error text unchanged for a bad input? Yes. Predictable errors need exact stable output, so the form shows the server error text unchanged next to each named input as escaped plain text, with a raw JSON mode as escape hatch.
- How does the Idempotency-Key behave across reload, second tab, edit, and in-flight retry? One key per form open. The console keeps the key for reload, second tab, and retry of the same values, and makes a new key after an edit. A resend with the same key and changed body shows the 409 conflict text unchanged.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
