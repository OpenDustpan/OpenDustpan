## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add conformance cases in NEW pkg/forge/gitlab for token open, self expiry, commits chain and ParseEvent deny. Check: `go test ./pkg/forge/gitlab/ -run TestGitLabConformance -race` fails on main.
- [ ] 1.2 Implement NEW pkg/forge/gitlab mapping. Check: `go test ./pkg/forge/gitlab/ -run TestGitLabMapping -race` passes.
- [ ] 1.3 Add the Retry-After clamp test in NEW pkg/forge/gitlab/retry_test.go for a huge and a malformed value. Check: `go test ./pkg/forge/gitlab/ -run TestRetryAfterClamp -race` in NEW `pkg/forge/gitlab/retry_test.go` fails on main, passes after.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire GitLab kind into the forge service opener map. Check: `go test ./pkg/forge/... -race` passes.
- [ ] 3.2 Expose GitLab kind on the same routes and actions. Check: `go test ./internal/api/server/ -run TestGitHost -race` passes.
- [ ] 3.3 Add e2e Check step. Check: E2E calls `POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check` with an access token against the fake GitLab.
- [ ] 3.4 Add e2e commits step, a Git host read through the connection. Check: BLOCKED by a route that lists commits: main has none under git-host-connections; the step runs the adapter ListCommits test against the fake GitLab.
- [ ] 3.5 BLOCKED by NEW `POST /webhooks/{hook ID}` public route: add e2e webhook step with X-Gitlab-Token event secret. Check: BLOCKED by NEW `POST /webhooks/{hook ID}` public route with event secret and no credential kind; e2e posts with X-Gitlab-Token and expects 401 on wrong token.
- [ ] 3.6 Document GitLab token setup and prove latency. Check: Docs commands run as written; `go test ./pkg/forge/gitlab/ -run TestCheckLatency -race` shows p95 under 800 ms.
- [ ] 3.7 Run usability check and full check. Check: `make check` reports zero findings.
