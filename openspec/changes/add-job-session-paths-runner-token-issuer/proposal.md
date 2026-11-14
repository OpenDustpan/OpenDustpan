Issue: #970.

## Why

**Persona: runner operator.** As a Runner operator I run a pool that executes jobs, and my launchers need a credential for the tool step that works on one unit only and stops when the claim stops.

The user path this change must make work:

1. **Server start.** The operator starts the server with a runner pool. A runner joins the pool and claims a job phase.
2. **API.** The runner claims a phase. The server makes the job token for the unit of the job, in the transaction of the claim.
3. **API.** A step presents the job token on the unit of its job. Another unit gives 403.
4. **API.** The job ends, the claim expires, a takeover replaces the claim or the runner is retired. The token stops and later use gives 401.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A step uses a job token after job end, claim expiry, takeover or retire. | {"code": 401, "message": "job token ended. Read the job state."} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| A step uses a job token on another unit. | {"code": 403, "message": "job token is for one unit only. Read the job to see its unit."} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- The constant identity.CredentialJobToken is on main, and job-token code exists in pkg/jobtoken/service.go. Proof: Pack code fact: rg -n -F --glob '!*_test.go' 'identity.CredentialJobToken' pkg internal cmd gives 5 matches, including pkg/jobtoken/service.go:202 and pkg/job/service_start.go:57.
- The session protocol types exist. Proof: Pack path pkg/runner/protocol exists.
- No caller of runner.SessionStore.OpenSession is on main, so this part adds no session route. Proof: Pack code fact: rg -n -F --glob '!*_test.go' 'runner.SessionStore.OpenSession' pkg internal cmd gives 0 matches.

## What Changes

- Make a job token at the claim of a runner phase, bound to the job ID and the stable unit ID.
- Check the token on the unit of the job only, until the end of the claim and the step grace period.
- End tokens at job end, claim expiry, takeover or retire of the runner.
- Set Principal.JobID for job tokens only.

Out of scope:

- Session exchange, renew, source reads, step records, log chunks, plan staging, credentials route, sweep report and finish (later parts)
- Session rate limit, runner and launcher binaries, sweep claimer wiring
- Sweep pools and their purpose (no sweep code in this part)

Depends on:

- #881
- #968

## Usability

Where the path still costs the person time or surprises them:

- The job record shows the claim of a token and when it ends.
- A refused token names the one API call that shows the job.

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

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (owner-tagged: decision 11).

- Decisions that apply: 8 (current), 11 (current), 12 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2. Chain to the end: ADR 204, ADR 350, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350.
- Quote: "Job sessions (owner). The launcher makes an Ed25519 key pair in memory. It never writes the private key to a disk."
- Quote: "The job token goes to the tool, step by step. The launcher asks for the credentials of each step of the role `tool` through its session."
- Effect: A token authenticates only while its claim is live, at most until the end of the step timeout and grace period. A retire ends the claim and so the token.
- Conflict: none.

**ADR 138: Runner actions, audit entries and the REST API** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "`credentials-issued` | the runner and the session | `runner:claim` | `reveal`"
- Effect: This part writes no credentials entry. The later credentials route writes it.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The design and spec say the job token authenticates only while its claim is live on the unit, but the Layers table and the Make requirement do not state the grace period of ADR 133 decision 12 (token valid until end of timeout and grace period of its step); the spec omits this bound, so the token may stop too early or too late versus ADR 133 decision 12. Resolution: answered: The ADR check and the End with claim scenarios now name the step grace bound.
- R1.2 (open question): Spec-ready gate fails: a code check reports E004 at spec.md:37 (two shall clauses in one sentence). The open questions section does not read none, so the change may not be implemented until the requirement is split. Resolution: answered: The requirement is split into single-sentence requirements, one SHALL each.
- R1.3 (missing scenario): No scenario covers a takeover that happens while a token is in use, a claim renewal that extends expiry, or a retire of the runner (ADR 133 decision 8 ends claims and job sessions). The end-with-claim requirement names takeover but has no retire or lease-loss scenario. Resolution: answered: Scenarios for takeover, retire and renew are added.
- R1.4 (failure mode): Restart and concurrency are not covered: the store must write token hash, job ID, unit reference and claim expiry in the claim transaction (design decision 1), but no scenario says what happens if the server restarts between claim and token store, or two claims race for one phase. The design lists no test for a duplicate make. Resolution: answered: The claim transaction rule, the rollback scenario and the race case are added.
- R1.5 (missing scenario): The error table gives a 401 message for a token used after claim expiry, but the spec has no scenario for an expired token with a clock skew, nor for a malformed or unknown token (the port adapter layer says fail closed, with no scenario). The hostile test 'replay an ended token with no state read' has no matching scenario or task. Resolution: answered: A fail closed requirement covers unknown, malformed and expired tokens.
- R1.6 (scope): Task 2.2 wires the issuer at tool auth, and task 3.1 requires an e2e step with a real runner claim and tool read. The proposal says the tool-step credentials route is out of scope (credentials route is a later part), so tool auth wiring and the e2e read path belong to another item; the task set does not match the split. Resolution: answered: Tool auth wiring is kept as the check point of the token. The credentials route stays out of scope, and the e2e step uses the claim and the tool state path only.
- R1.7 (wrong claim): The proposal claims issue 970 says no issue exists for the issuer, but the issue text says the job token issuer has no issue yet and cites ADR 100 decision 20 as the basis; the proof is a pack rg, not a checked command. The claim 'Pack rg has 5 hits' is not reproduced with an exact command. Resolution: answered: The evidence now cites the exact rg command of the pack.
- R1.8 (owner decision conflict): The change does not reference the owner decision P14 on sweep purpose; the change does not touch sweeps, so the conflict is not active, but the ADR check lists ADR 164 and ADR 343 nowhere, leaving the purpose-check boundary unrecorded. Resolution: answered: The considered list records that sweeps are out of scope and cites the owner decision.
- R1.9 (open question): Principal.JobID is set only for CredentialJobToken, but the change does not say how a job token is distinguished from a session or runner credential at the HTTP handler layer when the token is presented on a tool state path; the layer table says 403 but the spec has no scenario for a runner key on a tool path. Resolution: answered: A scenario for a runner key on a tool path is added.

## Open questions

none

Questions considered and answered:

- When does the server make and end a job token? ADR 100 decision 20 and ADR 133 decisions 8 and 12: make at claim, limit to the unit, end with the claim or at the step grace bound.
- Does this part add session routes or sweep code? No. ADR 133 decision 11 sessions are a later part. Owner decision, 13 Nov 2026 (queue P14) concerns sweep pools, which this part does not touch.
- What if two claims race? The token row is written in the claim transaction, so both or neither exist. One claim wins per phase, so one token exists.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
