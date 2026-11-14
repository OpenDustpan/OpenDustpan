## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing checks first

- [ ] 1.1 Show that the clients check fails on main when the generated files are stale. Check: make clients-check

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Update the docs pages and run their commands in a test. Check: make docs-lint
- [ ] 3.2 Regenerate the API description and clients. Check: make clients-check
- [ ] 3.3 BLOCKED by the pack: it names no job start route and no credential kind. The e2e step runs the page commands with the real CLI once the follow-up change names them. Check: make e2e
- [ ] 3.4 Check the usability of the page. Check: The developer completes the job start path from the docs page alone.
- [ ] 3.5 Run all checks. Check: make check
