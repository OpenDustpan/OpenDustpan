## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for version, bounds, subset, hash mismatch, replay and triage. Check: `go test ./internal/unmanaged -run TestReport`

## 2. Build

- [ ] 2.1 Build the reader, the encrypted stream write, the findings store and the routes with their actions. Check: `go test ./internal/api/runnerapi -run TestSweepReport`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run an e2e step with `POST /runner/v1/session/sweep-report` using a session proof, then list findings with an access token. BLOCKED by 160-165-sweep-targets-lifecycle for the sweep claim. Check: `make e2e`
- [ ] 4.2 Write the docs page and run the usability check. Check: `make docs-lint`
- [ ] 4.3 Run the full check. Check: `make check` with zero findings
