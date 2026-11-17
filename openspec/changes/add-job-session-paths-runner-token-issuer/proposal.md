---
version: 9
---

Issue: #970.

Task 2.2 (the SQL adapters of jobtoken.Store) is implemented by #2916. The owner decided on 17 Nov 2026 that the server makes the job token at the launcher request, not at the claim (ADR 386). This change makes the credentials path of the job session do that.

## Why

**Persona: runner operator.** As a Runner operator I run a pool that executes jobs. The tool step of a launcher needs a credential that works on one unit only and stops when the claim or the step stops.

The user path this change must make work:

1. **Server start.** The operator starts the server with a runner pool. A runner joins the pool.
2. **API.** The runner claims a phase through the existing runner protocol under /runner/v1. The claim answer holds no job token. The launcher opens its job session with POST /runner/v1/session and asks for the credentials of a step on the credentials path. The server makes the job token at that request and shows the plaintext once, in that answer.
3. **API.** A step sends the job token as a bearer access token (credential kind job-token, identity.CredentialJobToken) to the OpenTofu HTTP backend route /tofu/state/{namespace}/{workspace} of its own unit. Another unit gives 403.
4. **API.** The job ends, the claim expires, a takeover replaces the claim, the runner is retired or the step grace bound passes. The token stops and later use gives 401.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A step uses a job token after job end, claim expiry, takeover, retire or the step grace bound. | {"code": 401, "message": "job token ended. Read the job state."} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| A step uses a job token on another unit. | {"code": 403, "message": "job token is for one unit only. Read the job to see its unit."} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- The constant identity.CredentialJobToken is on main and is used by job-token code. Proof: Pack code fact: rg -n -F --glob '!*_test.go' 'identity.CredentialJobToken' pkg internal cmd gives 5 matches, including pkg/jobtoken/service.go:202 and pkg/job/service_start.go:57.
- `jobtoken.Service.Issue` has no caller outside tests, and the credentials path is declared but not routed. Proof: rg -n -F --glob '!*_test.go' 'PathSessionCredentials' internal pkg cmd gives only the declaration at pkg/runner/protocol/protocol.go:74.
- job.Claimer has Renew and Finish methods. Proof: Pack main list: job.Claimer.Renew at pkg/job/executor.go:165 and job.Claimer.Finish at pkg/job/executor.go:186.

## What Changes

- Make the job token at the launcher request on the credentials path of the job session, bound to the job ID, the stable unit ID and the claim, with an expiry that is the earlier of the claim end and the step timeout plus grace period (ADR 386, owner). The claim makes none.
- Write one audit entry for each request that names the job, the step and the expiry.
- Route the credentials path. The change `add-job-session-paths-runner-2` no longer routes it.
- Check the token on the state routes of the unit of the job only, at each use: the claim is live, the expiry has not passed, and the starter principal still holds its grants.
- Stop the token with its claim: each use reads the live claim, so job end, claim expiry, takeover and retire end it.
- Set Principal.JobID for a job token only.
- Hand out the plaintext once, in the credentials answer. Each request makes one token for its step; an earlier token of the claim stops at its own expiry or with the claim.

Out of scope:

- Session exchange (on main), renew route, source reads, step records, log chunks, plan staging, sweep report and finish route (add-job-session-paths-runner-2)
- The session rate limit, the runner and launcher binaries, the sweep claimer wiring
- Sweep pools (no sweep code in this part)

Depends on:

- job-token-authenticator-chain (merged, #3215; task 4.2 checks that the job token reaches its own unit and is refused on another unit through that chain)
- `runners` (the pool, the claim, the handoff token and the retire of a runner)
- the job session exchange, POST /runner/v1/session (on main, `internal/api/runnerapi/session.go`)

Builds on (not a change):

- #881
- #968

## Usability

Where the path still costs the person time or surprises them:

- The audit entry of each request shows the job, the step and when the token ends.
- A refused token names the one API call that shows the job. A person with job read reads it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- job.Claimer is the port of claim, renew and finish. (`pkg/job/executor.go:147`)
- job.Claimer.Renew exists. (`pkg/job/executor.go:165`)
- job.Claimer.Finish exists. (`pkg/job/executor.go:186`)
- The constant CredentialJobToken exists. (`pkg/identity/identity.go:113`)
- A job-token service sets the credential kind on a principal. (`pkg/jobtoken/service.go:202`)
- A job session opens through POST /runner/v1/session and calls runner.SessionStore.OpenSession. (`internal/api/runnerapi/session.go:293`)
- `jobtoken.Service.Issue` issues a token with an audit entry and has no caller outside tests. (`pkg/jobtoken/service.go:63`)
- The credentials path and the type `CredentialsResponse` are declared in the protocol. (`pkg/runner/protocol/protocol.go:74`, `pkg/runner/protocol/protocol.go:424`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 386: The server makes the job token when the launcher asks for step credentials** (owner-tagged, owner 2026-11-17).

- Decisions that apply: 1, 2, 3, 4, 5.
- Changed by: none yet.
- Current rule: this ADR, with ADR 100 decision 20 for the principal, the unit limit and the job-ID claim, and ADR 133 decisions 12 and 13.
- Quote: "The server makes the job token at the launcher request (owner, 2026-11-17). The launcher asks for step credentials through its job session. The server makes the job token at that request. The claim of a runner phase makes no job token. The claim answer holds none."
- Quote: "The expiry is the earlier of two times (owner, 2026-11-17). The first time is the end of the claim. The second time is the end of the step timeout plus the grace period. The token expires at the earlier time."
- Quote: "Each request leaves a record. The server writes one audit entry for each launcher request. The entry names the job, the step and the expiry."
- Effect: The credentials path makes the token with one audit entry per request. The claim makes none.
- Conflict: none. This ADR changes the timing of ADR 100 decision 20.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 20 (timing changed by ADR 386).
- Changed by: ADR 386 changes decision 20; ADR 213 changes decision 20 (principal and reach); ADR 304 changes decision 20 (principal and reach); other items of the chain do not touch decision 20.
- Current rule: ADR 386 for the time of the making; this ADR for the rest of decision 20.
- Quote: "The job token and the job-ID claim. At the claim of a runner phase, the server makes a **job token** for the steps of the phase."
- Effect: Only the time of the making moves, to the launcher request. The unit limit and the job-ID claim stay.
- Conflict: resolved by ADR 386.

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2; ADR 386 extends decisions 12 and 13. Chain to the end: ADR 204, ADR 350, ADR 386, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350, 386.
- Quote: "The job token goes to the tool, step by step. The launcher asks for the credentials of each step of the role `tool` through its session."
- Effect: The token authenticates only while its claim is live and at most until the end of the step timeout and grace period. The credentials path of the job session hands it to the launcher, and this change routes that path.
- Conflict: none.

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2. Chain to the end: ADR 204, ADR 350, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350.
- Quote: "A retired runner authenticates nothing."
- Effect: A retire ends the claim and so the token.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- When does the server make and end a job token? ADR 386 (owner, 2026-11-17): make at the launcher request on the job session, expiry the earlier of the claim end and the step timeout plus grace. ADR 100 decision 20 and ADR 133 decisions 8 and 12: limit to the unit, end with the claim.
- Does this part add session routes or sweep code? It routes the credentials path only. No sweep code. Owner decision, 13 Nov 2026 (queue P14) concerns sweep pools, which this part does not touch.
- How does the plaintext reach the caller when only a hash is stored? The make call returns the plaintext once, in the credentials answer. A repeated request makes a new token for its step.
- What if the starter loses grants after the claim? The token acts as the starter at each use, so the grant check runs at each use (ADR 100 decision 20).
- What if the claim ends before the request? The server refuses the request and makes no token, because the store checks that the claim is live in the transaction of the issue.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
