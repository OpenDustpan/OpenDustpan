---
version: 3
---

## Why

**Persona: runner operator.** A runner operator tries to run a runner pool for a namespace. Today the operator has no single page that shows the full path and the fix for each common error.

The user path this change must make work:

1. **Docs page.** Open the runner operator docs page and create a runner pool.
2. **API.** Create a join token and start a runner against the server API.
3. **API.** Scope the pool, watch claims and logs, then drain and retire through the server API.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Join fails because the join token was already used | Server returns `runner.token-used`. Make a new single-use token and join again. | `ANY /api/runners/{}/pools/{}/join-tokens/{}/revoke` |
| Pool or runner lookup fails | Server returns `runner.not-found`. List pools and check the pool id. | `ANY /api/runners/{}/pools` |
| Claim fails because the runner is retired | Server returns `runner.retired`. The retire is final, so join a new runner to the pool. | `ANY /api/runners/{}/runners/{}/retire` |
| Join fails because the join token expired or was revoked | Server returns `runner.invalid`. Make a new join token and join again. | `ANY /api/runners/{}/pools/{}/join-tokens/{}/revoke` |
| Pool gets no claims because it is scoped to the wrong namespace | No claims reach the pool. List pools and check the scope ids before more work. | `ANY /api/runners/{}/pools` |

Evidence for the claims above:

- Main has runner test helpers for join tokens. Proof: joinToken in pkg/runner/runnertest/runnertest.go:194 (func joinToken)
- Main has a store method for join tokens. Proof: JoinTokens in pkg/store/postgres/runner.go:419 (func Runners.JoinTokens)
- Main has the runner pool route. Proof: ANY /api/runners/{}/pools in route table or API description
- Main has the runner drain and retire routes. Proof: ANY /api/runners/{}/runners/{}/drain and ANY /api/runners/{}/runners/{}/retire in route table or API description
- Main has the join-token revoke route. Proof: ANY /api/runners/{}/pools/{}/join-tokens/{}/revoke in route table or API description
- Main has the runner protocol route. Proof: ANY /runner/v1 in route table or API description
- Main has runner error codes for used token, retired runner, unknown object, bad value, and limit. Proof: runner.token-used in internal/errcode/catalog_runner.go:27, runner.retired in internal/errcode/catalog_runner.go:24, runner.not-found in internal/errcode/catalog_runner.go:16, runner.invalid in internal/errcode/catalog_runner.go:10, runner.limit in internal/errcode/catalog_runner.go:13

## What Changes

- One runner operator docs page with the full path: create pool, join token, start runner, scope, watch claims and logs, drain, retire, and common errors.
- No new CLI command and no new route; pool work uses the existing runner API routes with an access token and runner work uses ANY /runner/v1 with a runner token.

Out of scope:

- Server behavior for runners, pools, claims, and tokens
- New CLI commands for runner management
- Tagging and publishing a release
- Install, upgrade, and SDK guides

Depends on:

- docs-commands-run-in-tests

## Usability

Where the path still costs the person time or surprises them:

- The operator copies a join token that expires before use and must make a new one.
- The operator scopes a pool to the wrong namespace and sees no claims.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Test helper makes a join token for runner tests. (`pkg/runner/runnertest/runnertest.go:194 (func joinToken)`)
- Test helper makes a join token at a fixed time. (`pkg/runner/runnertest/runnertest.go:202 (func joinTokenAt)`)
- Postgres store lists join tokens for runners. (`pkg/store/postgres/runner.go:419 (func Runners.JoinTokens)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which ADR decisions govern runner pools, join tokens, drain, and retire, and which routes and actions does the guide use?
   - Options: Ship the page using only pack-proven routes and codes; Block the page until the runner pool and token ADR decision lines are quoted.
   - Provisional decision (owner review 14 Nov): Block the page until the runner pool and token ADR decision lines are quoted
   - Reason: The pack adrs list is empty so no decision lines can be quoted; after a step back, shipping token guidance without quoted token decisions is not safe. Benchmark: AWS documents IAM actions per API call before use; the guide must do the same for runner routes
   - Persona impact: A runner operator sees no wrong grant or route; the page waits rather than misleads. Escape hatch: None; the page ships only after the ADR texts are quoted in adr_check.

Questions considered and answered:

- Does the guide need a new API route? No. The pack lists the pool, drain, retire, join-token revoke, and runner protocol routes, so the page shows those existing routes and adds no new route.
- Does the guide need a new error code? No. The page names only the runner codes proven in the pack catalog and adds no new code.
- Which ADRs govern runner pools and tokens? The pack adrs list is empty, so no ADR decision text is available to quote. The change quotes no ADR and changes no ADR. The blocking open question names the missing ADR texts.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
