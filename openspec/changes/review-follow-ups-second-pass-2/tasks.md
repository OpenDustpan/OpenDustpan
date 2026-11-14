## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the curlxffx case to internal/api/audit/export_cef_test.go Check: `go test ./internal/api/audit -run Export` fails on main if the case is missing
- [ ] 1.2 Run the rg row checks for the four guides on main Check: `rg -n "MaxPostSize|name_locks|sign counter|Commit statuses" docs/guides` lists the missing rows

## 2. Docs and script

- [ ] 2.1 Add the missing notes to the four guides Check: `rg -n "MaxPostSize|name_locks|sign counter|Commit statuses" docs/guides` shows each line
- [ ] 2.2 Fail on an empty version in scripts/bench-s3.sh Check: `sh -n scripts/bench-s3.sh` passes and an empty variable exits non-zero

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Record each row as added or skipped, and run the full check Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
