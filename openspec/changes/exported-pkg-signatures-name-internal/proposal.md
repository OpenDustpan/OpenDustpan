---
version: 1
---

Issue: #1577, #1578, #1584, #1711.

## Problem

An Embedder builds a third-party store against the SDK and cannot name the internal types in exported signatures, so the build fails.

## Evidence

- storetest ImportPorts names internal unitimport types. Proof: pkg/store/storetest/backup_imports.go:21:	Store      unitimport.Store
- workspace service names internal scopeaudit in an exported field. Proof: pkg/workspace/service.go:288:	OutOfScope scopeaudit.Recorder
- sqlite store implements the internal unitimport ports. Proof: pkg/store/sqlite/unitimport.go:20:	_ unitimport.Store     = (*Imports)(nil)
- job artifact blobs expose a family function that names scopeaudit. Proof: pkg/job/artifactblobs.go:49:func ArtifactBlobFamily() scopeaudit.Family {
- secret item key blobs expose a family function that names scopeaudit. Proof: pkg/secret/itemkeyblob.go:29:func ItemKeyFamily() scopeaudit.Family {

## Fix boundary

- Move unitimport port types to the NEW pkg/unitimport package and keep the service in internal/unitimport.
- Point exported pkg signatures at NEW pkg value types for scopeaudit.
- Land the NEW sdk-surface gate with full api.txt plus a leak baseline.

Out of scope:

- Splits of stack, workspace, job, secret, breakglass and trigger services beyond the signature fix
- The drift service move, now in internal/driftsvc
- Other findings of #1711

## Check

- Add a failing gate test for the baseline over pkg/store/storetest/backup_imports.go. Check: The gate test fails on main with the leaks of the issue and passes after the fix.
- Add a failing outside-module compile test that names the NEW pkg/unitimport Store and the BlobFamily functions of pkg/stack and pkg/job. Check: The outside-module compile test fails on main naming internal/unitimport and passes after the fix.
- Move Store, UnitStore, LedgerRow and UnitKey to the NEW file pkg/unitimport/unitimport.go and update storetest ImportPorts and the store adapters. Check: pkg/unitimport builds and the sqlite and postgres adapters compile with no cycle.
- Land the NEW gate internal/tools/sdksurface/surface.go with full api.txt plus a leak baseline. Check: `make sdk-surface` writes api.txt and fails on a new leak.
- Run the storetest backup round trip, update the docs page, run the usability check of the Embedder path, and run make check with zero findings. Confirmed by: CI (make check and make sdk-surface). Check: `make check` with zero findings and `make sdk-surface` passes.

Depends on:

- new-services-adapters-land-in-settings-collection-job

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- storetest declares ImportPorts for backup import ports. (`pkg/store/storetest/backup_imports.go:20 (type ImportPorts)`)
- job declares ArtifactBlobFamily for plan and log blobs. (`pkg/job/artifactblobs.go:49 (func ArtifactBlobFamily)`)
- secret declares ItemKeyFamily. (`pkg/secret/itemkeyblob.go:29 (func ItemKeyFamily)`)
- workspace declares StateBlobFamily for state blobs. (`pkg/workspace/stateblobs.go:31 (func StateBlobFamily)`)
- stack declares BlobFamily for the blob scope family. (`pkg/stack/blobscope.go:25 (func BlobFamily)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 272: The public SDK surface** (owner-tagged: decision 3).

- Decisions that apply: 4 (extended), 3 (current).
- Changed by: ADR 364 extends decision 4. Chain to the end: ADR 364.
- Current rule: this ADR, with ADR 364.
- Quote: "No exported signature in `pkg/` names a type from `internal/`."
- Quote: "The CI check `make sdk-surface` also writes `internal/tools/sdksurface/api.txt`, with one line for each exported SDK name and its signature."
- Effect: The change follows the no-leak rule by moving closure types to pkg and follows the snapshot rule by landing the surface gate.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where do the unitimport port types live ADR 272 decision 4: a type that the port closure reaches is SDK, so unitimport Store, UnitStore, LedgerRow and UnitKey move to pkg.
- How is the scopeaudit leak removed ADR 272 decision 4: scopeaudit Recorder, Where and Family named by exported pkg signatures move to pkg or the port stops naming them.
- What guards the surface after the fix ADR 272 decision 3: the NEW gate writes api.txt and fails when it is not up to date, so new leaks fail CI.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
