## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. tests first

- [ ] 1.1 Write failing tests for each spec scenario. Check: TestRestoreAccessSettingDefaultOff fails on main

## 2. build

- [ ] 2.1 Add the stored boolean key with fail-closed parse. Check: `go test ./pkg/setting/... -count=1`
- [ ] 2.2 Branch the restore apply step on the snapshot with the two-path guard. Check: `go test ./internal/backup/... -run TestRestoreAccess -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Route test lists the settings route with its actions. Check: Route test passes
- [ ] 4.2 E2E: console session changes the setting, then a restore runs over TLS. Check: `make e2e`
- [ ] 4.3 Add the docs page and the report listing. Check: Docs page and report show the value
- [ ] 4.4 Usability walk from the docs page alone. Check: Each error names one command
- [ ] 4.5 Run full checks. Check: `make check` has zero findings
