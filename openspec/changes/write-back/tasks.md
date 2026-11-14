## 1. Failing tests first

- [ ] 1.1 Add failing spec tests for commit with trailer, refusal of non-config paths, stale base, and retryable forge error. Check: `go test ./internal/jobtest/ -run TestWriteBackCommitsEditWithTrailer -count=1`
- [ ] 1.2 Add failing conformance tests of the Forge fake for the retryable and refused classes. Check: `go test ./internal/jobtest/ -run TestWriteBackEditMatrix -count=1`

## 2. Definition of done

- [ ] 2.1 Implement the write-back deliver phase on the durable runner with base-commit read and job trailer. Check: `go test ./internal/jobsvc/ -run TestWriteBack -count=1`
- [ ] 2.2 Wire the edit route with the unit write action and the audit entry. Check: `go test ./internal/api/ -run TestWriteBackRoutes -count=1`
- [ ] 2.3 Add e2e step with the real binary over TLS against the fake forge: edit, commit with trailer, plan shows edit, retryable error retries, refused error fails. Check: `make e2e E2E_RUN=write-back` against the fake GitHub forge
- [ ] 2.4 Write the docs page for API config edits and write-back commits. Check: add the new file docs/guides/unit-config-edits.md and check each command renders as written
- [ ] 2.5 Run the usability check from the docs page alone and the full check. Check: `make check` with zero findings
