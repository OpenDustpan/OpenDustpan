## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add NEW internal/unitimport/tfeapi/source_test.go with TestTfeApiSourceConformance that runs RunSource with a seeded fixture and a bad credential. Check: TestTfeApiSourceConformance fails on main. No route: the test drives the adapter against a local TFE fake only.
- [ ] 1.2 Add NEW internal/unitimport/tfeapi/hostile_test.go for private address, cross-host redirect, oversize body, timeout and 5xx. Check: Hostile tests fail on main. No route: the test drives the adapter against a local TFE fake only.

## 2. Build

- [ ] 2.1 Write NEW internal/unitimport/tfeapi/source.go with the NEW credential type and the Locker capability. Check: Both test files pass. No route: the test drives the adapter only.
- [ ] 2.2 Register the NEW error codes import.source-denied, import.source-not-found and import.source-unavailable in the NEW file internal/errcode/catalog_import.go. Check: The catalog test lists the three codes. No route: the test reads the catalog file only.
- [ ] 2.3 Add the e2e step in NEW e2e/import_tfe_test.go with server S as TFE-compatible source and server D as target. Check: The step passes with the real CLIs on SQLite and PostgreSQL; it calls `ANY /api/v2/state-versions/{}/resources` on server S with an access token and passes its read gate. Confirmed by: CI (e2e).
- [ ] 2.4 Update docs/guides/import.md for the tfe-api kind. Check: The page names tfe-api and the tested source only. No route: the test reads the guide file only.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run conformance, e2e, docs lint and the full check. Check: make check reports zero findings. No route: the test runs static checks only. Confirmed by: CI (make check).
