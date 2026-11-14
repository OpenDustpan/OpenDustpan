## 1. failing tests first

- [ ] 1.1 Add failing port tests for ParseEvent signature check, two-secret overlap and effect-state duplicate rule. Check: TestForgeWebhookDedupe fails on main, passes after the fix.
- [ ] 1.2 Add failing forgetest cases for Head, ReadFile, Compare, FindCommit, OpenReview, ReadReview and CloseReview. Check: TestGitHubFilesReviewsCapabilities fails on main, passes after the fix.
- [ ] 1.3 Add failing test that the runner git path refuses an unscoped token and accepts a scoped token of at most 1 hour. Check: TestRepoTokenScopedRefused fails on main, passes after the fix.

## 2. implementation

- [ ] 2.1 Implement ParseEvent and the verifier with two stored secrets from one snapshot and effect-state duplicate check. Check: `go test ./pkg/forge/... -run TestParseEvent -count=1` passes.
- [ ] 2.2 Implement Head, ReadFile, Compare, FindCommit and review reads with capability guards; hold Commit until the signing spike lands. Check: `go test ./pkg/forge/github/... -count=1` passes.
- [ ] 2.3 Implement RepoToken with one-repository scope, contents read and 1-hour cap. Check: `go test ./pkg/forge/... -run TestRepoToken -count=1` passes.
- [ ] 2.4 Wire POST /webhooks/{hook ID} outside /api/dustpan/v1 with the reject metric. Check: POST /webhooks/{hook ID} answers 204 for good, bad and replayed deliveries in the test server.

## 3. definition of done

- [ ] 3.1 Record route with its public listing and no stored grant use. Check: Route test lists no auth actions for POST /webhooks/{hook ID} and the public list holds it.
- [ ] 3.2 Add e2e step with signed delivery, replay and bad-signature cases. Check: E2E job drives a signed push delivery against the real binary over TLS and checks 204 plus hint record.
- [ ] 3.3 Write docs section for hook setup, secrets, dedupe bound and replica drop. Check: Intake docs section lists the intake behavior, the 204 rule and the use of the stable hook hostname.
- [ ] 3.4 Run usability check from the docs page alone and full check. Check: `make check` reports zero findings.
