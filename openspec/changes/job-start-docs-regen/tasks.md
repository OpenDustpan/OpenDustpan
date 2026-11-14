## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing checks first

- [ ] 1.1 Show that the clients check fails on main when the generated files are stale: change nothing, then run `make clients-check` (the `Makefile` target), which compares clients/typescript with a fresh run of the generator and write the result in the pull request body. Check: make clients-check

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Update the job start docs page under docs/guides/ (the existing guide that covers job start; name its file in the pull request) and run its commands in a test in a NEW test file internal/clientgen/golang/docs_commands_test.go. Check: make docs-lint
- [ ] 3.2 Regenerate the API description and clients. Check: make clients-check. Confirmed by: the driver's gates.
- [ ] 3.3 BLOCKED by the pack: it names no job start route and no credential kind. The e2e step runs the page commands with the real CLI once the follow-up change names them. Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 Check the usability of the page. Check: The developer completes the job start path from the docs page alone. A person's step.
- [ ] 3.5 Run all checks. Check: make check. Confirmed by: CI (the agent runs the fast checks only).
