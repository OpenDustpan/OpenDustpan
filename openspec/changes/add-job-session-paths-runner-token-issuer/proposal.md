---
version: 2
---

Issue: #970.

## Why

**Persona: runner operator.** As a Runner operator I run a pool that executes jobs. The tool step of a launcher needs a credential that works on one unit only and stops when the claim or the step stops.

The user path this change must make work:

1. **Server start.** The operator starts the server with a runner pool. A runner joins the pool.
2. **API.** The runner claims a phase through the existing runner protocol path /runner/v1. The server stores the job token record in the claim transaction. The server shows the plaintext once, only to the code that makes it.
3. **API.** A step sends the job token as a bearer access token (credential kind job-token) to the state routes of its own unit: the Pulumi Cloud-compatible API or the OpenTofu HTTP backend. Another unit gives 403.
4. **API.** The job ends, the claim expires, a takeover replaces the claim, the runner is retired or the step grace bound passes. The token stops and later use gives 401.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A step uses a job token after job end, claim expiry, takeover, retire or the step grace bound. | {"code": 401, "message": "job token ended. Read the job state."} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| A step uses a job token on another unit. | {"code": 403, "message": "job token is for one unit only. Read the job to see its unit."} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- The constant identity.CredentialJobToken is on main and is used by job-token code. Proof: Pack code fact: rg -n -F --glob '!*_test.go' 'identity.CredentialJobToken' pkg internal cmd gives 5 matches, including pkg/jobtoken/service.go:202 and pkg/job/service_start.go:57.
- No code opens a job session through runner.SessionStore.OpenSession, so this part adds no session route. Proof: Pack code fact: rg -n -F --glob '!*_test.go' 'runner.SessionStore.OpenSession' pkg internal cmd gives 0 matches.
- job.Claimer has Renew and Finish methods. Proof: Pack main list: job.Claimer.Renew at pkg/job/executor.go:165 and job.Claimer.Finish at pkg/job/executor.go:186.

## What Changes

- Store a job token record at the claim of a runner phase, bound to the job ID and the stable unit ID, with an expiry that is the earlier of the claim end and the step timeout plus grace period.
- Check the token on the state routes of the unit of the job only, at each use: the claim is live, the expiry has not passed, and the starter principal still holds its grants.
- End the token in the same transaction as job end, claim expiry, takeover or retire.
- Set Principal.JobID for a job token only.
- Hand out the plaintext once, to the caller that makes it. A retry of the claim makes a new token record and ends the earlier record of that claim.

Out of scope:

- Session exchange, renew route, source reads, step records, log chunks, plan staging, credentials route, sweep report and finish route (later parts)
- The session rate limit, the runner and launcher binaries, the sweep claimer wiring
- Sweep pools (no sweep code in this part)

Depends on:

- #881
- #968

## Usability

Where the path still costs the person time or surprises them:

- The job record shows the claim of a token and when it ends.
- A refused token names the one API call that shows the job. A person with job read reads it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- job.Claimer is the port of claim, renew and finish. (`pkg/job/executor.go:147`)
- job.Claimer.Renew exists. (`pkg/job/executor.go:165`)
- job.Claimer.Finish exists. (`pkg/job/executor.go:186`)
- The constant CredentialJobToken exists. (`pkg/identity/identity.go:113`)
- A job-token service sets the credential kind on a principal. (`pkg/jobtoken/service.go:202`)
- runner.SessionStore.OpenSession is a port method with no HTTP caller. (`pkg/runner/store.go:177`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 20 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "The job token and the job-ID claim. At the claim of a runner phase, the server makes a **job token** for the steps of the phase."
- Effect: The issuer makes the job token at claim, limited to the unit of the job.
- Conflict: none.

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2. Chain to the end: ADR 204, ADR 350, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350.
- Quote: "The job token goes to the tool, step by step. The launcher asks for the credentials of each step of the role `tool` through its session."
- Effect: The token authenticates only while its claim is live and at most until the end of the step timeout and grace period. The session route that hands it to a launcher is a later part.
- Conflict: none.

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2. Chain to the end: ADR 204, ADR 350, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350.
- Quote: "A retired runner authenticates nothing."
- Effect: A retire ends the claim and so the token.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Wire the issuer into job.Claimer Claim, Renew and Finish. The route /t" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- When does the server make and end a job token? ADR 100 decision 20 and ADR 133 decisions 8 and 12: make at claim, limit to the unit, end with the claim or at the step grace bound.
- Does this part add session routes or sweep code? No. Owner decision, 13 Nov 2026 (queue P14) concerns sweep pools, which this part does not touch.
- How does the plaintext reach the caller when only a hash is stored? The make call returns the plaintext once. A retry makes a new record and ends the old one.
- What if the starter loses grants after the claim? The token acts as the starter at each use, so the grant check runs at each use (ADR 100 decision 20).
- What if two claims race? The token is written in the claim transaction, so one claim wins per phase and one live token exists.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
