---
version: 1
---

Issue: #1302, #1303, #1304, #1305.

## Problem

A namespace administrator connects a GitLab repository and checks that the connection works, reads commits, and receives events. Today only the GitHub adapter exists, so a GitLab token has no adapter to open, no expiry to show, and no event check.

## Evidence

- GitHub adapter exists on main. Proof: pkg/forge/github
- Connection routes exist on main. Proof: ANY/GET/POST /api/dustpan/v1/namespaces/{}/git-host-connections in the route table
- Check route exists on main. Proof: POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check in the route table
- Conformance suite exists on main. Proof: pkg/forge/forgetest/forgetest.go:151 (interface method SetTokenExpiry)

## Fix boundary

- Add NEW pkg/forge/gitlab adapter with token auth, self expires_at, commits id parent_ids last_commit_id mapping, and X-Gitlab-Token event check.
- Wire GitLab kind into connection open and Check expiry answer; keep routes and authorization actions unchanged.
- Return ErrUnsupported for Reviews, Tokens, and Rotation; deny forged or missing events with 401 and unknown hook IDs with 404 on POST /webhooks/{hook ID}.

Out of scope:

- Credential expiry warnings at 30 and 7 days and RotateSelf durable rotation.
- Per-namespace GitHub App and token credential sets with PATCH address change, address digest, and 409 rule.
- namespace:use-git-host-connection checks in self-service and code location routes and delete-in-use rule.

## Check

- Add conformance cases in NEW pkg/forge/gitlab for token open, self expiry, commits chain and ParseEvent deny. Check: `go test ./pkg/forge/gitlab/ -run TestGitLabConformance -race` fails on main.
- Implement NEW pkg/forge/gitlab mapping. Check: `go test ./pkg/forge/gitlab/ -run TestGitLabMapping -race` passes.
- Add the Retry-After clamp test in NEW pkg/forge/gitlab/retry_test.go for a huge and a malformed value. Check: `go test ./pkg/forge/gitlab/ -run TestRetryAfterClamp -race` in NEW `pkg/forge/gitlab/retry_test.go` fails on main, passes after.
- Wire GitLab kind into the forge service opener map. Check: `go test ./pkg/forge/... -race` passes.
- Expose GitLab kind on the same routes and actions. Check: `go test ./internal/api/server/ -run TestGitHost -race` passes.
- Add e2e Check step. Check: E2E calls `POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check` with an access token against the fake GitLab.
- Add e2e commits step, a Git host read through the connection. Check: BLOCKED by a route that lists commits: main has none under git-host-connections; the step runs the adapter ListCommits test against the fake GitLab.
- BLOCKED by NEW `POST /webhooks/{hook ID}` public route: add e2e webhook step with X-Gitlab-Token event secret. Check: BLOCKED by NEW `POST /webhooks/{hook ID}` public route with event secret and no credential kind; e2e posts with X-Gitlab-Token and expects 401 on wrong token.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- GitHub adapter implements commits with moved-head retry. (`pkg/forge/github/commit_test.go:920 (func TestCommitFollowsAMovedBranchAtMostThreeTimes)`)
- Server lists git host connections. (`internal/api/server/githost_connections.go:172 (func Handler.listGitHostConnections)`)
- Server has namespace git host routes. (`internal/api/server/githost.go:115 (func Handler.gitHostNamespaceRoutes)`)
- Forge retry helper exists. (`pkg/forge/forge.go:455 (func RetryAfter)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 271: The Forge port, Git host connections and the installation-level GitHub App** (owner-tagged: decision 1, 2, 4, 8, 11).

- Decisions that apply: 1 (current), 2 (current), 4 (changed), 8 (current), 11 (extended), 12 (current).
- Changed by: ADR 286 changes; ADR 299 changes decision 4; ADR 299 extends decision 2; ADR 299 extends decision 7; ADR 300 changes decision 7; ADR 303 extends decision 11; ADR 336 extends. Chain to the end: ADR 286, ADR 299, ADR 300, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 286, 299, 300, 303, 336.
- Quote: "The port lives in `pkg/forge`, and that package owns the connections."
- Quote: "One interface with capabilities. `Opener` makes a `Forge` for one connection and one credential set, and makes no call to the Git host."
- Quote: "An event is a hint. `ParseEvent` is in the port, because each Git host signs its events in its own way."
- Quote: "The person or IaC gives the event secret. The event secret belongs to the app registration, not to a credential set or a connection."
- Quote: "User-facing text says "GitHub" or "Git host", never "forge"."
- Quote: "A credential is bound to its address."
- Effect: Adds the NEW GitLab adapter beside pkg/forge/github. Implements the full Forge interface behind capabilities, with unsupported groups returning ErrUnsupported. Reuses POST /webhooks/{hook ID} with the write-only event secret. Binds each open to the stored address; address digest and PATCH stay out of scope.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the GitLab adapter live? ADR 271 decision 1: NEW pkg/forge/gitlab, with no net/http or SQL in pkg/forge.
- Which Forge capability groups does GitLab implement? ADR 271 decision 2: Files read, Commits list and Events ParseEvent; Reviews, Tokens and Rotation return ErrUnsupported.
- How are GitLab events authenticated without polling? ADR 271 decisions 4 and 8 and ADR 299 decision 29: webhooks only; X-Gitlab-Token is compared in constant time.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
