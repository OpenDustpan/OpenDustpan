---
version: 1
---

Issue: #1720.

## Problem

The error-code catalog imports domain packages to build the Forbidden answer, so the catalog depends on layers it should not know.

## Evidence

- The issue reports internal/errcode/errcode.go on main imports pkg/gate, pkg/identity, pkg/ipallow and internal/sessionauth to build the Forbidden answer. Proof: Issue #1720 body: internal/errcode/errcode.go on main imports pkg/gate, pkg/identity, pkg/ipallow and internal/sessionauth to build the Forbidden answer.
- The paths internal/errcode, pkg/gate, pkg/identity, pkg/ipallow and internal/sessionauth exist on main. Proof: paths: internal/errcode, internal/errcode/errcode.go, internal/sessionauth, pkg/gate, pkg/identity, pkg/ipallow each exists true
- No ADR is touched; the pack ADR list is empty. Proof: Pack field adrs is an empty list.
- Callers build answers from plain code and values through errcode.New. Proof: internal/api/problem/problem.go:284:	made, err := errcode.New(p.Code, p.Values...)

## Fix boundary

- internal/errcode imports no pkg/... domain package and no internal/sessionauth
- Forbidden mapping lives on the adapter side and passes plain fields to errcode.New
- Goldens are unchanged and an import guard test keeps the catalog free of domain imports

Out of scope:

- Change of any Forbidden answer text or golden
- Change of any other error-code mapping

## Check

- Add the NEW import guard test `TestCatalogHasNoDomainImports` in internal/errcode that denies imports of pkg/gate, pkg/identity, pkg/ipallow and internal/sessionauth. Check: The NEW test `TestCatalogHasNoDomainImports` fails on main and passes after the fix: `go test ./internal/errcode -run TestCatalogHasNoDomainImports -count=1`.
- Move the Forbidden domain-error mapping from internal/errcode/errcode.go to internal/api/problem and internal/api/refusal with errors.As matching in the order gate, identity, ipallow, sessionauth, and pass plain fields to errcode.New. Check: `go test ./internal/errcode ./internal/api/problem ./internal/api/refusal -count=1` passes plus `go build ./...`.
- Delete the removed errcode helpers from internal/errcode/errcode.go and update each caller found by rg to pass plain code and values. Check: `rg -n -F errcode.New pkg internal cmd` lists only plain-field callers plus `go build ./...`.
- Run the affected unit tests, the golden check, and make check. Check: `go test ./internal/errcode ./internal/api/... -count=1` passes, goldens are unchanged and `make check` reports zero findings. Confirmed by: CI (make check).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has errcode.New for building answers from code and values. (`internal/errcode/answer.go:102 (func New)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Where should the Forbidden mapping live? In internal/api/problem and internal/api/refusal, which already build answers. They read the domain error and pass plain code and values to errcode.New. The pack lists no ADR, so the issue text decides.
- Which single guard keeps the catalog free of domain imports? One import guard test in internal/errcode. It refuses direct imports of the banned packages. It keeps the change inside the touched package and adds no lint config.
- Does this change touch any ADR? No. Pack field adrs is an empty list, so no ADR decision lines apply.
- How are wrapped errors and two matching types handled? errors.As matches wrapped errors, so answers stay the same as on main. The order is gate, identity, ipallow, sessionauth; first match wins. Only a domain error with no match uses the generic Forbidden fallback.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
