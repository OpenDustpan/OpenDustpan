---
version: 2
---

## Why

**Persona: namespace administrator.** A namespace administrator sets where unit work runs. They make a runner pool, hand a short join token to the team that hosts runners, and retire hosts that leave. Without managed pools, joins stay manual and a removed pool can strand work.

The user path this change must make work:

1. **API.** Create a runner pool for jobs in the namespace from the console or API.
2. **CLI.** Create a join token for the pool and give it to the runner host.
3. **Console.** Join the runner, list pools and runners, drain or retire a runner.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller without manage-runners creates a pool or a join token. | 403 runner action refused; ask a namespace administrator to grant manage-runners, then check reach with the caller API. | `opendustpan runner pools list --namespace <ns>` |
| Delete of a pool that a live job or a sweep target names. | 409 runner.pool-in-use; remove the job or sweep target first, then delete the pool. | `opendustpan runner pools list --namespace <ns>` |
| Second join with one join token. | 410 runner.token-used; create a new join token with the runner management API. | `dustpan-runner join --server <URL> --pool <id>` |

Evidence for the claims above:

- Main has runner management routes and join-token and retire handlers. Proof: internal/api/runnerapi/routes.go:92 (func Handler.managementRoutes)
- Main defines runner error codes for pool-in-use, retired, and token-used. Proof: internal/errcode/catalog_runner.go:22 (error code runner.pool-in-use)
- Main tests join token single use in the runner conformance suite. Proof: pkg/runner/runnertest/cases.go:166 (func testJoinTokenSingleUse)

## What Changes

- The NEW service in internal/runnersvc manages pools, join tokens, drain, and retire with fenced SQL adapters.
- Management routes enforce namespace:read-runners and namespace:manage-runners and single-use join tokens.
- Pool delete refused with runner.pool-in-use while named; retire expires claims in the same transaction.
- The NEW command opendustpan runner pools list lists pools of a namespace.

Out of scope:

- Claim polling, heartbeats, and renewals.
- Launcher step start and job sessions.
- Source filter and sweep claims.
- Sweep targets and sweep runs.

## Usability

Where the path still costs the person time or surprises them:

- Pool purpose jobs versus sweeps is easy to pick wrong; the form names the identity each purpose uses.
- Join secret shows once; the console warns to copy it before closing.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Runner domain with conformance suite exists on main. (`pkg/runner/runnertest/cases.go:166 (func testJoinTokenSingleUse)`)
- Join token store accessor exists on main. (`pkg/store/postgres/runner.go:419 (func Runners.JoinTokens)`)
- Join-token list handler exists on main. (`internal/api/runnerapi/admin.go:393 (func Handler.listJoinTokens)`)
- Retire runner handler exists on main. (`internal/api/runnerapi/admin.go:675 (func Handler.retireRunner)`)
- Runner error codes for pool and token states exist on main. (`internal/errcode/catalog_runner.go:22 (error code runner.pool-in-use)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 130: Runner pools, runners and coordinators** (owner-tagged: decision 3, 6).

- Decisions that apply: 3 (changed), 6 (current), 8 (current), 10 (current).
- Changed by: ADR 164 changes decision 5; ADR 300 changes; ADR 343 changes decision 3. Chain to the end: ADR 164, ADR 300, ADR 343, ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 164, 300, 343.
- Quote: "A pool has a purpose that never changes (owner). The purpose is `jobs` or `sweeps` (`runner.Purpose`). The server takes the durable kinds that a runner claims from the pool of the runner, never from the runner (`runner.ClaimKinds`): - A runner of a pool for jobs claims only `job.runner.<pool ID>`. - A runner of a pool for sweeps claims only `unmanaged.sweep.<pool ID>`.  A change of the purpose fails with `runner."
- Quote: "One claim at a time by default (owner). A pool has a concurrency from 1 to 64 (`Pool.Concurrency`). The default is 1. Each runner of the pool holds at most that many claims. A Kubernetes runner usually needs a higher value, because each claim is a Job of its own, not a process on the runner."
- Quote: "Limits. A namespace has at most 50 pools, a pool at most 1,000 runners that are not retired, and at most 100 open join tokens."
- Quote: "A delete of a pool in use fails. A delete of a pool fails with `runner.ErrPoolInUse` while a job that is not terminal or a sweep target names it. A delete of a pool also deletes its runners and its join tokens."
- Effect: Follows purpose set that never changes, concurrency default, pool and token and runner limits, and refuse of pool delete in use.
- Conflict: none.

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (owner-tagged: decision 2).

- Decisions that apply: 2 (extended), 3 (current), 8 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2. Chain to the end: ADR 204, ADR 350, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350.
- Quote: "Join tokens (owner). A principal with `namespace:manage-runners` creates a join token for one pool. The token is good for one join."
- Quote: "The secret starts with `dpj_`, so secret scanners find it. The server shows the secret once, and keeps only its SHA-256."
- Quote: "Retire a runner. A principal with `namespace:manage-runners` can drain a runner or retire it. A retired runner authenticates nothing."
- Effect: Follows single-use join token with dpj_ prefix and SHA-256 storage, join in one transaction, drain and retire with claim expiry.
- Conflict: none.

**ADR 138: Runner actions, audit entries and the REST API** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 6 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "`namespace:manage-runners` of ADR 103 covers the create, the change and the delete of a pool, the create and the revoke of a join token, the drain and the retire of a runner"
- Quote: "`pool-created`, `pool-changed`, `pool-deleted` | the principal | `namespace:manage-runners` | `change` |"
- Quote: "Routes. The tool-neutral OpenDustpan API gets these routes. Each route lands with its e2e step:"
- Effect: Follows management actions, audit entries in the same transaction, and management routes for pools, tokens, and runners.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Follows ADR 343: sweeps run on a runner; pool purpose set holds jobs or sweeps or both and never changes after create.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What limits apply and what happens on delete in use and token reuse? ADR 130 decisions 8 and 10 and ADR 133 decision 2 answer them; enforced in store transaction and service.
- Which actions guard pool and token routes? ADR 138 decisions 1 and 6 answer it: read versus manage routes with namespace:read-runners and namespace:manage-runners.
- May a pool serve jobs and sweeps, and may its purpose change? ADR 130 decision 3 with ADR 343: the purpose set holds jobs or sweeps or both and never changes; a change fails with runner.immutable.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
