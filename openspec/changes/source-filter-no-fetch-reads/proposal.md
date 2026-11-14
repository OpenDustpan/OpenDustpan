---
version: 1
---

Issue: #1994.

## Problem

A namespace administrator sets failOnDenied or narrows submodule patterns and believes fetches obey it, but no fetch reads the stored filter while the docs promise an effect.

## Evidence

- The reference page promises that a new filter applies to later fetches. Proof: Issue #1994 cites docs/reference/source-filter-api.md:28 with that promise; the pack confirms path docs/reference/source-filter-api.md exists.
- No production code outside tests reads the filter port; the only non-test matches are the conformance suite. Proof: Pack code fact `rg -n -F --glob '!*_test.go' 'Filters.Filter' pkg internal cmd` lists 6 matches, all in pkg/source/sourcetest/sourcetest.go:171,214,235,343,394,408.

## Fix boundary

- Say on docs/reference/source-filter-api.md that the server stores the filter and no fetch reads it yet
- Move filter encode, audit and fence steps from internal/api/sourcefilter into a small service in internal
- Correct the comment that claims checks run in the service and in the store

Out of scope:

- Enforce the filter in any fetch or snapshot path
- Change the filter shape or the failOnDenied field
- Change GET and PUT shapes, error codes, or audit behavior
- Rename the audit action name to namespace:manage-source-filter (set by source-filter-routes-use-runner; this change lands first)

## Check

- Add a service test that a filter write keeps fence use and one audit record Check: NEW `TestSetKeepsFenceAndAudit` in NEW internal/sourcefiltersvc/service_test.go fails on main
- Add a handler test that the PUT route calls the service with an access token under its existing gate, written in NEW internal/api/sourcefilter/service_call_test.go Check: NEW `TestPutCallsTheService` in NEW internal/api/sourcefilter/service_call_test.go on PUT /api/dustpan/v1/namespaces/{namespace}/source-filter fails on main before the move
- State on docs/reference/source-filter-api.md that the server stores the filter and no fetch reads it yet Check: Read docs/reference/source-filter-api.md and see the stored-only sentence
- Move encode, audit and fence steps from the handler into NEW internal/sourcefiltersvc and correct the wrong service-and-store comment Check: rg shows no direct store-port write from internal/api/sourcefilter/handlers.go
- File the follow-up issue that removes the stored-only note when a fetch path first reads the filter Check: Follow-up issue for fetch enforcement and note removal exists with its number recorded in tasks.md
- Verify wiring on PUT /api/dustpan/v1/namespaces/{namespace}/source-filter with an access token under the existing gate, then docs page and usability check. Confirmed by: CI (`make check` passes with zero findings). Check: `make check` passes with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The Filters port exposes a filter read for a namespace. (`pkg/source/filters.go:30 (func Filters.Filter)`)
- The postgres store exposes the source-filter port. (`pkg/store/postgres/sourcefilter.go:21 (func Store.SourceFilters)`)
- The server wires source-filter support through a dustpan option. (`pkg/dustpan/sourcefilter.go:15 (func WithSourceFilters)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does a stored-filter note or a handler-to-service move need an ADR or RFC first? No. The pack lists no ADR and no RFC for this item, and the fix keeps the stored filter shape and adds no fetch enforcement.
- Where does the stored-only status show at runtime? Docs page alone, because the GET and PUT shapes, codes and audit behavior do not change and no runtime flag is added.
- How does the stored-only note get removed when fetch enforcement lands? A follow-up issue tracks removal when a later fetch path reads the filter, so the note cannot go stale without an owner.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
