## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing masked-read scenario tests per tool in the NEW files pkg/stack/masked_read_test.go and pkg/workspace/masked_read_test.go Check: NEW TestMaskedReadReplacesSecrets in stack and workspace suites fails on main
- [ ] 1.2 Add failing reveal scenario tests with audit entry check in the NEW file pkg/stack/reveal_test.go Check: NEW TestRevealOnePathOneEntry fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Build masked ReadMasked and single-path Reveal behind unit:read and unit:read-secrets Check: `go test -race ./pkg/stack/... ./pkg/workspace/...` passes with an access token
- [ ] 3.2 Wire the NEW tool-neutral routes with declared actions Check: route test passes for the NEW `GET /api/dustpan/v1/units/{ref}/state` with an access token and declared actions
- [ ] 3.3 Add Pulumi e2e step against the real CLI Check: e2e step calls existing `/api/stacks/{org}/{project}/{stack}/state/{version}/resources` with an access token after a real Pulumi CLI update
- [ ] 3.4 Add OpenTofu e2e step against the real CLI Check: e2e step runs real OpenTofu apply then calls the NEW `GET /api/dustpan/v1/units/{ref}/state` with an access token; BLOCKED by the NEW route until wired
- [ ] 3.5 Write docs page for masked read and reveal Check: docs page command runs as written from a clean install
- [ ] 3.6 Run usability check Check: Developer completes the path from the docs page alone, each error names one command
- [ ] 3.7 Run full check Check: `make check` with zero findings
