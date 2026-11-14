## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test in the NEW file pkg/locale/locale_test.go for same accept and reject of the moved checks including timeZone. Check: The NEW test TestLocaleChecksMatchMain in the NEW file pkg/locale/locale_test.go fails on main because the NEW package pkg/locale is absent
- [ ] 1.2 Add failing test in the NEW file pkg/preferences/validators_test.go that custom Validators replace the digits check. Check: The NEW test TestPreferencesValidatorsReplaceChecks in the NEW file pkg/preferences/validators_test.go fails on main because the NEW symbol WithValidators is absent
- [ ] 1.3 Add failing import test in pkg/preferences/deps_test.go that pkg/preferences imports no internal package. Check: The NEW test TestPreferencesImportsNoInternal in pkg/preferences/deps_test.go fails on main because pkg/preferences/check.go imports internal/locale

## 2. Fix

- [ ] 2.1 Move the four checks to the NEW file pkg/locale/locale.go and switch pkg/preferences/check.go to call the NEW package pkg/locale. Check: The NEW package pkg/locale passes `go test ./pkg/locale/... ./pkg/preferences/...`
- [ ] 2.2 Add Validators port and NEW WithValidators in the NEW file pkg/preferences/validators.go with the moved checks as default. Check: The NEW symbol WithValidators passes `go test ./pkg/preferences/...` with custom digits accepted
- [ ] 2.3 Change internal/locale/format.go to thin wrappers that call the NEW package pkg/locale. Check: `go test ./internal/locale/...` passes
- [ ] 2.4 Sweep pkg/ imports for internal/ use and record the result and the ADR 35 note in the NEW file pkg/locale/doc.go. Check: The NEW file pkg/locale/doc.go records `rg -n opendustpan.org/opendustpan/internal/ pkg/` output; note states a public pkg package must not import internal for behavior an SDK user may need
- [ ] 2.5 Guard transitive internal/ imports in the NEW file pkg/locale/deps_test.go and in pkg/preferences/deps_test.go. Check: The NEW package pkg/locale passes `go list -deps ./pkg/preferences ./pkg/locale` with no internal import

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire check: call GET /api/dustpan/v1/me/preferences and PATCH /api/dustpan/v1/me/preferences after the move. Check: GET /api/dustpan/v1/me/preferences with a session credential answers the same document as on main
- [ ] 4.2 Add the new file docs/reference/preferences.md with valid values and WithValidators use. Check: Embedder builds a scratch module that calls and replaces the four checks from the docs page alone
- [ ] 4.3 Usability check of the docs page path. Check: Embedder completes the user path from the docs page alone and each error names the one command that helps
- [ ] 4.4 Run full checks. Check: `make check` passes with zero findings
