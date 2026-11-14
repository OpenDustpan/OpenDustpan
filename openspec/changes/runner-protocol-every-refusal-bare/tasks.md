## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server tests for missing header, bad release, malformed body, large body, bad proof on poll and session, check order, dev self-accept, and old release against route ANY /runner/v1 with a runner credential. Check: TestRunnerRefusalMissingRelease, TestRunnerRefusalBadRelease, TestRunnerRefusalBadBody, TestRunnerRefusalBodyTooLarge, TestRunnerRefusalBadProof, TestRunnerDevSelfAccept, TestRunnerOldReleaseUnsupported fail on main in internal/api/runnerapi

## 2. Fix

- [ ] 2.1 Map each refusal class to its status and NEW error code in NEW file internal/api/runnerapi/refusal.go. Check: TestRunnerRefusalMissingRelease passes against route ANY /runner/v1 with a runner credential
- [ ] 2.2 Accept the server release string as identical, including dev, before the semver parse in pkg/runner/version.go:128 (func CheckVersionAhead). Check: TestRunnerDevSelfAccept passes on a dev build string in pkg/runner/version.go:128 (func CheckVersionAhead)
- [ ] 2.3 Enforce the 1 MiB body limit at the runnerapi boundary with a bounded reader in NEW file internal/api/runnerapi/refusal.go. Check: curl 30 MiB body against ANY /runner/v1 returns 413 runner.body-too-large
- [ ] 2.4 Register each NEW error code in internal/errcode/catalog_runner.go and regenerate the API description. Confirmed by: CI (make check). Check: rg runner.missing-release internal/errcode/catalog_runner.go shows each NEW code. Confirmed by: CI (make check).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Update the runner API reference and docs page in the same pull request. Check: Reference runner-api page shows each code, status, and the dev note
- [ ] 4.2 Run a usability check of the full refusal path from the docs page alone. A person's step. Check: Runner operator completes join, poll, and session against a dev build from the docs page alone
- [ ] 4.3 Run the definition of done: wiring, route ANY /runner/v1 with a runner credential, e2e step against the real directory, docs page, usability check, make check. A person's step. Check: `make check` with zero findings
