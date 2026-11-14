---
version: 1
---

Issue: #2462.

## Problem

Empty principal source still maps to oidc in the principal package while the sign-in service refuses it. The store input of the sign-in service is untyped any with run-time assertions.

## Evidence

- The composition file of the sign-in service exists. Proof: paths: pkg/dustpan/area_signin.go exists true
- Main holds the principal sign-in method. Proof: pkg/principal/service.go:744 (func Service.signIn)
- principal.invalid is the existing code for bad principal input. Proof: internal/errcode/catalog_principal.go:11 principal.invalid
- No non-test text spells principal.signIn in the searched tree. Proof: rg -n -F --glob '!*_test.go' 'principal.signIn' pkg internal cmd matches 0

## Fix boundary

- Service.signIn in pkg/principal refuses an empty principal source with principal.invalid and keeps no oidc fallback.
- signInServiceInputs.store in pkg/dustpan/area_signin.go uses a small interface for the methods the service needs.

Out of scope:

- Change of sign-in methods or providers
- Change of group claim handling

## Check

- Add the NEW test TestSignInRefusesEmptySource in the NEW file `pkg/principal/signin_empty_source_test.go` as a direct call of Service.signIn with an empty source. Check: TestSignInRefusesEmptySource fails on main and passes after the fix.
- Add the NEW test TestSignInValidSourceStillSignsIn in the NEW file `pkg/principal/signin_empty_source_test.go` as a direct call of Service.signIn with a valid source. Check: TestSignInValidSourceStillSignsIn passes before and after the fix.
- Add a compile-time assertion that the production store satisfies the narrow interface, and type the field in `pkg/dustpan/area_signin.go`. Check: go build ./pkg/dustpan fails with a mismatched store and passes with the production store.
- Wire the empty-source guard at the start of Service.signIn. No route and no docs page change. Check: The two tests above pass; the HTTP mapping is unchanged.
- Run the full checks with `make check` with zero findings. Confirmed by: CI (`make check`). Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main holds Service.signIn with the fallback. (`pkg/principal/service.go:744 (func Service.signIn)`)
- Main maps the provider mode to a method kind. (`pkg/session/provideroauth.go:38 (func ProviderMode.MethodKind)`)
- Main holds the code principal.invalid. (`internal/errcode/catalog_principal.go:11`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Keep the empty-means-oidc fallback or fail closed? Fail closed with the existing code principal.invalid. The issue says the sign-in service already refuses an empty source, so the fallback is a second, weaker rule. The pack supplies no ADR text.
- Type the store argument or keep any? Type it with a small interface. The compiler then checks the wiring and run-time assertions go away. Behavior does not change.
- Does an ADR touch this change? The pack supplies no ADR text to quote, so adr_check stays empty.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
