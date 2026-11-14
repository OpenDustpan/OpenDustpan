---
version: 1
---

Issue: #1333, #1336.

## Problem

The data of each context read section shows as an object with any member, so a generated client holds no typed value for it. Real answers are never checked against their schemas, so a nil slice can write null where the schema states an array.

## Evidence

- The context read answer covers eight sections. Proof: Issue #1333: "`objectcontext.Answer.Data` holds one of eight types, by section: identity, context, readme, outputs, dependencies, changes, health and audit."
- The description shows data as an object with any member. Proof: Issue #1333: "The description shows `data` as an object with any member (`ContextAnswer.data`)."
- No check proves that a real answer fits its schema. Proof: Issue #1336: "Nothing proves that a real answer fits its schema."
- The unit context read route exists on main. Proof: Pack coverage matrix lists route `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context` with op `getUnitContext`.
- The namespace context read route exists on main. Proof: Pack coverage matrix lists route `GET /api/dustpan/v1/namespaces/{namespace}/context` with op `getNamespaceContext`.
- The unknown-section error code exists on main. Proof: Pack main lists error code `context.unknown-section` at `internal/errcode/catalog_context.go:17`.

## Fix boundary

- The API description shows one named data schema for each of the eight context read sections, with unchanged wire members.
- Real answers of the namespace and unit context read routes validate against the description, with arrays never null.

Out of scope:

- Change of the wire members of context read answers
- New context sections
- Change of the refusal shape for an unknown section

## Check

- Write the failing test of the eight named section schemas in the NEW file pkg/dustpan/apidesc_context_sections_test.go against GET /api/dustpan/v1/openapi.json. Check: `go test ./pkg/dustpan -run TestPerSectionContextSchemas -count=1` fails on main
- Write the failing validation test with an inline subset validator in the NEW file pkg/dustpan/apidesc_context_answers_test.go. It reads real answers of GET getUnitContext and GET getNamespaceContext with an access token, including empty arrays and omitted pointer members. Check: `go test ./pkg/dustpan -run TestContextAnswersMatchDescription -count=1` fails on main
- Add the eight named section schemas to the API description and normalize nil slices in the NEW unexported answer constructor in pkg/objectcontext. Check: `go test ./pkg/dustpan ./pkg/objectcontext -count=1` passes
- Verify GET getUnitContext and GET getNamespaceContext with an access token and GET getApiDescription, update the context read docs page for the per-section shapes, and run make check with zero findings. A person's step. Check: `GET /api/dustpan/v1/openapi.json` lists the eight named schemas; `make check` passes with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the context reader and its IDOf helper. (`pkg/objectcontext/reader.go:257 (func IDOf)`)
- Main checks that description schemas agree with Go types. (`pkg/dustpan/apidesc_bodies_test.go:188 (func TestDescriptionSchemasAreConsistent)`)
- Main tests that sections name an action. (`pkg/objectcontext/objectcontext_test.go:269 (func TestSectionsNameAnAction)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the fix change the wire form of the context read answer? No. The description gains eight named data schemas. The JSON members stay the same.
- Does the Go-type consistency test prove that real answers fit their schemas? No. Issue #1336 states that nothing proves it, so the change adds a test on real answers for null arrays and omitted pointer members.
- How is the data member tied to the requested section without a oneOf? One named schema for each section, without a discriminated oneOf. A read of several sections answers one section entry for each requested section, and each data member validates against the named schema of its own section name.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
