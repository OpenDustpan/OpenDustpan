## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add failing tests in pkg/preferences/patch_test.go for host-only names, refused local names, bad shape and case, UTC, repeat of the stored value, and the same answer with ZONEINFO set and unset. Check: The NEW test TestPreferencesTimeZoneUsesEmbeddedDatabaseOnly in pkg/preferences/patch_test.go fails on main and passes after the fix.
- [ ] 1.2 Replace the host-dependent load layer in pkg/preferences/check.go with a lookup in the generated embedded zone list. Check: The NEW test TestEmbeddedZoneListMatchesTzdata passes in both directions and `rg -n LoadLocation pkg/preferences --glob '!*_test.go'` finds no match.
- [ ] 1.3 Wire the generator with a go generate directive in pkg/preferences, a NEW make target that runs internal/zonegen, and a CI freshness check. Check: The NEW make target rebuilds the list from internal/zonegen and the build fails when the list is stale.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Verify the existing PATCH /api/dustpan/v1/me/preferences path with an access token uses the new check; no openapi change. Check: PATCH /api/dustpan/v1/me/preferences with an access token stores an embedded zone and refuses a host-only zone with 422 preferences.field-invalid.
- [ ] 3.2 Update the preferences docs page in the same pull request. Check: The docs page for timeZone names the embedded database as the source.
- [ ] 3.3 Run the usability check from the docs page and make check. Check: `make check` reports zero findings.
