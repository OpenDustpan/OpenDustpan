---
version: 7
---

Issue: #4056

## Why

**Persona: runner operator.** A runner operator runs a pool of runners that must get work without an open port. Today the runner protocol has no poll or heartbeat path, so a runner cannot claim work or hold a lease while it runs.

The user path this change must make work:

1. **Server start.** Install and join a runner to a pool, then start the runner binary against the server.
2. **API.** The runner polls the existing route `POST /runner/v1/poll` with its runner key. On failure, run `opendustpan status`.
3. **API.** The runner renews the claim through the existing route `POST /runner/v1/claims/{operation}/renew` after a third of the pool lease, and sends the existing heartbeat `POST /runner/v1/heartbeat` every 15 s.
4. **Docs page.** Read the runner claims docs page and run its poll loop as written.

Errors the person may hit, and the one command that helps. The codes are the short protocol codes of `/runner/v1` that main already returns (`pkg/runner/protocol/protocol.go:198-214`). The change adds no second error shape.

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Poll with a bad runner proof fails. | Server answers 401 with `bad-proof`, with no key echo in body or logs. Check the key, the clock and the pool, then run `opendustpan status`. | `opendustpan status` |
| Poll with a retired runner fails. | Server answers 403 with `retired`. Join a new runner, then run `opendustpan status`. | `opendustpan status` |
| Poll comes too often. | Server answers 429 with `rate-limited` and `Retry-After`. Wait for the delay. | `opendustpan status` |
| Renew after the lease ended. | Server answers 409 with `claim-lost`. The runner stops the work. | `opendustpan status` |

Evidence for the claims above:

- Claimed-operation lease behavior exists on main. Proof: pkg/durable/runner_claimed_test.go:16
- Protocol error codes exist on main. Proof: pkg/runner/protocol/protocol.go:198 (const ErrorBadProof)
- Lease renew by holder exists on main. Proof: pkg/audit/audittest/feed.go:391 (func testLeaseRenewByHolder)
- Poll, heartbeat and claim renew routes exist on main. Proof: internal/api/runnerapi/routes.go:83 (func Handler.runnerRoutes)
- The pool lease exists on main. Proof: pkg/runner/runner.go:269 (func Pool.EffectiveLease)
- Runner protocol base route exists on main. Proof: route table or API description
- Runner protocol package exists on main. Proof: pkg/runner/protocol

## What Changes

- Add the runner-side poll, renew and heartbeat loop. It calls only the routes that main has: `POST /runner/v1/poll`, `POST /runner/v1/heartbeat` and `POST /runner/v1/claims/{operation}/renew`. The server routes, the error codes and the lease do not change.
- The lease of a claim is the lease of its pool, `Pool.EffectiveLease` (default 90 s, from `runner.MinLease` to `runner.MaxLease`). The loop renews after `runner.RenewEvery` of that lease. The change sets no fixed lease.
- Ship the NEW command `opendustpan runner poll` that runs the loop.
- Write the runner claims docs page.

Out of scope:

- Report upload and the end of a claim by report. The sweep claimer and the report route are the work of `add-job-session-paths-runner-sweep-claimer-wiring` and `160-165-report-intake-findings`.
- Launcher step start and step validation
- Source filter enforcement
- Runner pool and join token management
- Sweep target lifecycle and finding triage

Depends on:

- `130-138-runner-pools-management` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Runner operator must learn two new paths at once: poll and heartbeat; the docs page shows one copy-paste loop.
- Empty poll answer (204 after the wait) can look like a fault; the docs page says that 204 is normal and the loop polls again.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The poll route waits at most `protocol.MaxPollWait` (50 s) and answers 204 with no work. (`pkg/runner/protocol/protocol.go:36 (const PathPoll; MaxPollWait at :146)`)
- The claim renew route renews a claim before the session opens. (`pkg/runner/protocol/protocol.go:44 (const PathClaimRenew)`)
- The heartbeat route exists. (`pkg/runner/protocol/protocol.go:32 (const PathHeartbeat)`)
- Lease renew is checked by holder on main. (`pkg/audit/audittest/feed.go:391 (func testLeaseRenewByHolder)`)
- Server start steps exist on main. (`pkg/dustpan/start.go:66 (func Server.startSteps)`)
- Source filter option exists on main. (`pkg/dustpan/sourcefilter.go:15 (func WithSourceFilters)`)
- Postgres source filters read exists on main. (`internal/store/postgres/sourcefilter.go:21 (func Store.SourceFilters)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current), 12 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "A sweep is not a job. A job belongs to one unit, and the one-change rule, the phases and the gates of ADR 100 serve changes to a unit. A sweep changes nothing and has no unit. A sweep uses the same parts as a job: the runner pools, the ru"
- Quote: "The durable operation. The start of a sweep starts one durable operation of the kind `unmanaged.sweep.<pool ID>`, with the start key `<sweep ID>`, in the transaction that stores the sweep. A runner of the pool claims it through the runner"
- Effect: Claim polling reuses runner pools and runner claims for sweeps and jobs. The report upload is out of this change.
- Conflict: none.

**ADR 131: The runner protocol** (owner-tagged for decisions 5 and 9).

- Decisions that apply: 5 (owner), 7, 8, 12 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139.
- Current rule: this ADR, with ADR 139.
- Quote: "A poll waits for work for at most 50 seconds (`protocol.MaxPollWait`), below the idle timeout of common proxies."
- Quote: "A runner sends a heartbeat every 15 seconds, also while it polls."
- Quote: "The holder renews after a third of the lease of the pool (ADR 130 decision 7)."
- Quote: "Each answer with a status of 400 or more has the body `{\"code\": \"...\", \"message\": \"...\"}` (`protocol.Error`)."
- Effect: The loop uses the 50 s poll, the 15 s heartbeat, the renew after a third of the pool lease and the short codes of decision 12. The change adds no route, no code and no lease.
- Conflict: none. The earlier draft with a fixed 60 s lease, the `runner.*` codes and the upload route conflicted with decisions 7 and 12. This version removes them.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Sweeps run on a runner. Claim polling and heartbeat therefore serve sweep work on the same path as job work.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Do claims need a lease with renew and fence? Yes. RFC 6 states claims are leases, the runner renews the lease while it works, expiry returns the work to another runner, and the claim is a write in the namespace through the fence. The change follows that rule with the lease of the pool, a renew after a third of it, the 15 s heartbeat and the 50 s long poll that ADR 131 decides.
- Does polling need a push channel? No new tool. The runner opens every connection and long-polls the server per RFC 6. The change adds no push channel.
- Does this change add routes? No. Poll, heartbeat and renew exist on main, and the lead decided (option A) that the change extends them and adds no duplicate route.
- Where does the report upload go? Out of this change. Main has the port (`runner.SweepClaimer.Report`, `pkg/runner/store.go:247`) and the path (`protocol.PathSessionSweepReport`, `pkg/runner/protocol/protocol.go:115`). ADR 164 decision 12 says one streamed upload ends the claim. The earlier draft's resumable staged upload has no ADR. It is not part of this change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
