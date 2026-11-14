---
version: 1
---

Issue: #1274.

## Problem

Embedder builds their own server binary with the SDK and reuses the preferences checks. The checks live in internal/locale, which outside modules cannot import, so the embedder cannot call or replace them.

## Evidence

- `pkg/preferences` holds the checks file named in the issue Proof: paths: pkg/preferences/check.go exists true
- `pkg/preferences` checks use `internal/locale` for language, formattingLocale and digits Proof: issue #1274 body: It imports `internal/locale` for the checks of `language`, `formattingLocale` and `digits` (`pkg/preferences/check.go`).
- `pkg/preferences` is part of the public SDK and outside modules cannot import internal packages Proof: issue #1274 body: `pkg/preferences` is part of the public SDK (ADR 35). A Go package outside this module cannot import an `internal` package.

## Fix boundary

- The NEW public package pkg/locale in the NEW file pkg/locale/locale.go holds the language, formattingLocale, digits and timeZone checks as NEW ValidateLanguage, NEW ValidateFormattingLocale, NEW ValidateDigits and NEW ValidateTimeZone with the same behavior
- pkg/preferences in pkg/preferences/check.go calls the NEW package pkg/locale and no longer imports internal/locale for those checks
- NEW Validators port with NEW WithValidators in the NEW file pkg/preferences/validators.go lets an embedder replace the checks
- internal/locale in internal/locale/format.go keeps thin wrappers over the NEW package pkg/locale
- Recorded sweep of pkg/ imports and a note for ADR 35 in the NEW file pkg/locale/doc.go

Out of scope:

- Change of check behavior or valid values
- Public API for other internal helpers that SDK callers do not need

## Check

- Add failing test in the NEW file pkg/locale/locale_test.go for same accept and reject of the moved checks including timeZone. Check: The NEW test TestLocaleChecksMatchMain in the NEW file pkg/locale/locale_test.go fails on main because the NEW package pkg/locale is absent
- Add failing test in the NEW file pkg/preferences/validators_test.go that custom Validators replace the digits check. Check: The NEW test TestPreferencesValidatorsReplaceChecks in the NEW file pkg/preferences/validators_test.go fails on main because the NEW symbol WithValidators is absent
- Add failing import test in pkg/preferences/deps_test.go that pkg/preferences imports no internal package. Check: The NEW test TestPreferencesImportsNoInternal in pkg/preferences/deps_test.go fails on main because pkg/preferences/check.go imports internal/locale
- Move the four checks to the NEW file pkg/locale/locale.go and switch pkg/preferences/check.go to call the NEW package pkg/locale. Check: The NEW package pkg/locale passes `go test ./pkg/locale/... ./pkg/preferences/...`
- Add Validators port and NEW WithValidators in the NEW file pkg/preferences/validators.go with the moved checks as default. Check: The NEW symbol WithValidators passes `go test ./pkg/preferences/...` with custom digits accepted
- Change internal/locale/format.go to thin wrappers that call the NEW package pkg/locale. Check: `go test ./internal/locale/...` passes
- Sweep pkg/ imports for internal/ use and record the result and the ADR 35 note in the NEW file pkg/locale/doc.go. Check: The NEW file pkg/locale/doc.go records `rg -n opendustpan.org/opendustpan/internal/ pkg/` output; note states a public pkg package must not import internal for behavior an SDK user may need
- Guard transitive internal/ imports in the NEW file pkg/locale/deps_test.go and in pkg/preferences/deps_test.go. Check: The NEW package pkg/locale passes `go list -deps ./pkg/preferences ./pkg/locale` with no internal import

Depends on:

- check-time-zone-preference-against
- `check-time-zone-preference-against` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `pkg/preferences` holds a formattingLocale check (`pkg/preferences/check.go:131 (func formattingLocale)`)
- `pkg/preferences` holds a digits check (`pkg/preferences/check.go:139 (func digits)`)
- `pkg/preferences` has a language tag test (`pkg/preferences/zone_test.go:198 (func TestLanguageTags)`)
- `pkg/preferences` has a digits error test that names the valid values (`pkg/preferences/patch_test.go:205 (func TestDigitsErrorNamesTheValidValues)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 35: The SDK lives in pkg/, internal code in internal/** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 272 changes decision 1. Chain to the end: ADR 272, ADR 364.
- Current rule: this ADR, with ADR 272.
- Quote: "Code that is not part of the SDK lives under `internal/`. The HTTP adapters move to `internal/api/<tool>`. The Go toolchain then stops other modules from importing them."
- Effect: Follows decision 2. The three checks move to the NEW package `pkg/locale`. `pkg/preferences` stops importing `internal/locale` for those checks. `internal/locale` keeps thin wrappers that call the NEW package `pkg/locale`.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Move the checks to a public package or add a port in pkg/preferences so an SDK user replaces the checks? Both. The NEW package `pkg/locale` holds the pure checks so any module calls them. `pkg/preferences` adds a Validators port whose default calls the NEW package `pkg/locale`, so an embedder keeps current behavior or replaces a check. ADR 35 decision 2 places code that other modules need outside `internal/`.
- What happens to other users of internal/locale and to other pkg/ packages that import internal/? `internal/locale` keeps thin wrappers that call the NEW package `pkg/locale`, so one implementation remains. Server code that uses `internal/locale` keeps behavior. A follow sweep task runs rg over `pkg/` for `internal/` imports and records the result in the NEW file `pkg/locale/doc.go` with the ADR 35 note.
- Does ADR 364 add a decision to this change? No entry. The pack gives ADR 364 with no numbered decisions, so there is no decision line to quote or apply.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
