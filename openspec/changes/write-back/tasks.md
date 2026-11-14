## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing spec tests for commit with trailer, refusal of non-config paths, stale base, and retryable forge error. Check: `go test ./internal/jobsvc/ -run TestWriteBackCommitsEditWithTrailer -count=1`
- [ ] 1.2 Add failing conformance tests of the Forge fake for the retryable and refused classes. Check: `go test ./pkg/forge/forgetest/ -run TestForgeFakeRetryClasses -count=1`

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Implement the write-back deliver phase on the durable runner with base-commit read and job trailer. Check: `go test ./internal/jobsvc/ -run TestWriteBack -count=1`
- [ ] 3.2 Wire the edit route with the unit write action and the audit entry. Check: `go test ./internal/api/ -run TestWriteBackRoutes -count=1`
- [ ] 3.3 Add e2e step with the real binary over TLS against the fake forge: edit, commit with trailer, plan shows edit, retryable error retries, refused error fails. Check: BLOCKED by the job start route of add-job-service-routes-start-1 and the fake GitHub forge connection credential of docs/guides/connect-github.md; once both exist, run `make e2e E2E_RUN=write-back`.
- [ ] 3.4 Write the docs page for API config edits and write-back commits. Check: Add the new file docs/guides/unit-config-edits.md and check each command renders as written.
- [ ] 3.5 Run the usability check from the docs page alone and the full check. Check: `make check` with zero findings
