---
version: 1
---

Issue: #1334.

## Problem

An Embedder reads the API description to build a typed client, but fixed-set string members show as plain type string with no listed values.

## Evidence

- The context API already declares an open enum for ObjectRole. Proof: internal/api/objectcontext/operations.go:100:		apidesc.DeclareOpenEnum[objectcontext.ObjectRole]("ContextObjectRole",
- Notify already has a named LinkKind string type with fixed values. Proof: pkg/notify/notify.go:174:type LinkKind string

## Fix boundary

- Convert this closed member list to named Go string types with constants and DeclareOpenEnum route-label entries: Unit.status, UnitState.state, ManualLock.kind, Job.state, Job.executor, Job.operation, Job.wait, JobPhase.kind, JobApproval.verdict, NotificationSink.kind, NotificationSink.platform, preference theme, preference colorMode, preference hourCycle, preference dateStyle.
- Keep every other string member as plain string with no runtime validation change.
- Regenerate the description and affected clients with unchanged wire values.

Out of scope:

- New API routes or new member values
- Closed enums that reject unknown values at the description layer
- Runtime validation changes

## Check

- Write failing open-enum tests for inventory, jobs, sink, and preferences sets in internal/api/apidesc/schema_test.go. Check: `go test ./internal/api/apidesc/ -run TestReflectorAgreesWithTheEncoder -count=1`
- Convert each member on the closed convert list to a named Go type with constants and DeclareOpenEnum route-label entries, then regenerate internal/api/apidesc/openapi.json. Confirmed by: CI (schema checks). Check: `go test ./pkg/dustpan/ -run TestDescriptionSchemasAreConsistent -count=1`
- Check the wired description read and update the docs page with the closed convert list and the stays-string rule. Check: `curl $SERVER/api/dustpan/v1/openapi.json` shows open enums for converted members and plain string for other members
- Rebuild affected generated clients and run their build checks for the Embedder path. Check: Generated Go and TypeScript clients build with unchanged wire values
- Run the definition of done: description tests, wired description read, docs page and generated-client usability check. Confirmed by: CI (make check). Check: `make check` with zero findings

Depends on:

- `client-generator-python-template-set` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks open enums in the API description reflector. (`internal/api/apidesc/schema_check.go:439 (func reflector.checkOpenEnum)`)
- Main tests that the reflector agrees with the encoder. (`internal/api/apidesc/schema_test.go:309 (func TestReflectorAgreesWithTheEncoder)`)
- Main tests that description schemas stay consistent. (`pkg/dustpan/apidesc_bodies_test.go:188 (func TestDescriptionSchemasAreConsistent)`)
- Main defines ManualLock in the inventory package. (`pkg/inventory/inventory.go:90`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should fixed string sets become open enums or stay plain strings? Fixed sets become named Go string types with constants and DeclareOpenEnum label entries; free-text members stay plain string. The pack shows the pattern with ObjectRole declared at internal/api/objectcontext/operations.go:100 and LinkKind typed at pkg/notify/notify.go:174.
- Which members does the change cover? The closed member-level convert list in what_changes converts; every other string member stays plain string with unchanged runtime behavior.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
