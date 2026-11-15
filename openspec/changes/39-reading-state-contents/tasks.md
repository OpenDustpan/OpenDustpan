## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing masked-read scenario tests per tool in the NEW files pkg/stack/masked_read_test.go and pkg/workspace/masked_read_test.go Check: NEW TestMaskedReadReplacesSecrets in stack and workspace suites fails on main
- [ ] 1.2 Add failing reveal scenario tests with audit entry check by extending the existing file pkg/stack/reveal_test.go Check: NEW TestRevealOnePathOneEntry (new behavior) fails on main

- [ ] 1.3 SECURITY. Add failing hostile tests for authorize at point of use in the existing file pkg/stack/reveal_test.go (NEW TestRevealDeniedWithOnlyUnitRead) and in the NEW file pkg/stack/masked_read_test.go (NEW TestMaskedReadDeniedWithoutGrant). Check: a read with no grant and a reveal with only unit:read give 403 with no secret bytes; both tests fail on main
- [ ] 1.4 SECURITY. Add failing hostile tests for the boundary check in the NEW file pkg/stack/masked_read_test.go (NEW TestMaskedReadBoundaryRefusals). Check: a bad version, a bad JSON Pointer, a forged cursor, an expired cursor and a cursor of another caller are each refused, with no secret echo; the test fails on main
- [ ] 1.5 SECURITY. Add a failing hostile test for fail closed in the existing file pkg/stack/reveal_test.go (NEW TestRevealFailsClosed). Check: with a broken audit write and with a broken open, reveal answers 500 and returns no bytes; the test fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Build masked ReadMasked and single-path Reveal behind unit:read and unit:read-secrets Check: `go test -race ./pkg/stack/... ./pkg/workspace/...` passes with an access token
- [ ] 3.8 SECURITY. Build the boundary check for version, value path and cursor in the NEW file pkg/stack/masked_cursor.go. It allowlists the version, parses the JSON Pointer strictly, and verifies the cursor HMAC, expiry, unit, version and caller. Check: the tests of tasks 1.3, 1.4 and 1.5 pass
- [ ] 3.2 Wire the NEW tool-neutral routes with declared actions. The declaration file is NEW internal/api/apidesc/units_state.go. The adapter file is NEW internal/api/units_state.go. Check: route test passes for the NEW `GET /api/dustpan/v1/units/{ref}/state` with an access token and declared actions
- [ ] 3.3 Add Pulumi e2e step against the real CLI in the NEW file e2e/39_reading_state_contents_pulumi_test.go, which calls the existing route `GET /api/stacks/{org}/{project}/{stack}/state/{version}/resources` with an access token. Check: e2e step calls existing `/api/stacks/{org}/{project}/{stack}/state/{version}/resources` with an access token after a real Pulumi CLI update. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 Add OpenTofu e2e step against the real CLI in the NEW file e2e/39_reading_state_contents_tofu_test.go, which calls the NEW route `GET /api/dustpan/v1/units/{ref}/state` (declaration file NEW internal/api/apidesc/units_state.go, adapter file NEW internal/api/units_state.go) with an access token. Check: e2e step runs real OpenTofu apply then calls the NEW `GET /api/dustpan/v1/units/{ref}/state` with an access token; BLOCKED by the NEW route until wired. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.5 Write the NEW docs page docs/guides/reading-state-contents.md for masked read and reveal. Check: docs page command runs as written from a clean install. Confirmed by: CI (an e2e step in the NEW file e2e/39_reading_state_contents_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 3.6 Run usability check Check: Developer completes the path from the docs page alone, each error names one command. A person's step.
- [ ] 3.7 Run full check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
