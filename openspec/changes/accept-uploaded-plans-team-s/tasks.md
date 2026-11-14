## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for each spec scenario. Check: `go test ./pkg/job/ -run TestUploadedPlan -count=1` fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the route with its authorization actions, the e2e step with the real OpenTofu CLI, the docs page and the usability check. Check: BLOCKED by the owner ADR for the hash binding and by the upload route, which does not exist on main; then `make check` with zero findings and `make e2e`.
