---
version: 1
---

Issue: #1193.

## Problem

The API description lists schemas but shows no request or response samples, so use of the REST API takes longer than needed.

## Evidence

- Main checks that description schemas stay consistent. Proof: TestDescriptionSchemasAreConsistent at pkg/dustpan/apidesc_bodies_test.go:188

## Fix boundary

- Add request example to each operation with a JSON request body in the API description
- Add response example to each JSON success answer with a body in the API description
- Check each example against its schema and reject secret-like fixtures at generation time and at server start

Out of scope:

- Docs page task of #1190
- Console rendering in OpenDustpan/console
- Schemas themselves from #1189
- Tool-compatible Pulumi and OpenTofu APIs

## Check

- In NEW pkg/dustpan/apidesc_examples_test.go add failing test NEW TestDescriptionExamplesDecode that every example validates against its schema Check: NEW TestDescriptionExamplesDecode fails on main, passes after fix
- In NEW pkg/dustpan/apidesc_examples_test.go add hostile test NEW TestDescriptionExamplesHoldNoSecrets that scans nested objects and arrays for denied secret patterns Check: NEW TestDescriptionExamplesHoldNoSecrets rejects secret-like fixtures
- In NEW pkg/dustpan/apidesc_examples.go build NEW exampleRegistry through NEW newExampleRegistry, and serve the examples on the existing route GET /api/dustpan/v1/openapi.json through its adapter internal/api/server/openapi.go Check: The answer of GET /api/dustpan/v1/openapi.json holds one example per JSON body and none for 204 answers
- Measure description growth against the quality budget Check: Size check on GET /api/dustpan/v1/openapi.json: difference with and without examples stays below 500 KB
- Run usability read of examples from the description text alone Check: Embedder copies one request example from the description and decodes it with the generated client
- Run full checks Check: `make check` passes with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests that description schemas are consistent. (`pkg/dustpan/apidesc_bodies_test.go:188 (func TestDescriptionSchemasAreConsistent)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR constrain request and response examples in the API description? No ADR text in the pack covers the API description, so the adr_check list stays empty.
- Does this change include the docs page or the console rendering of examples? No. The issue says the docs page is a task of #1190 and the console part is a separate issue in OpenDustpan/console.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
