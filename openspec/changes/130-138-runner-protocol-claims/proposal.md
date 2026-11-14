---
version: 3
---

## Why

**Persona: runner operator.** A runner operator runs a pool of runners that must get work without an open port. Today the runner protocol has no poll or heartbeat path, so a runner cannot claim work or hold a lease while it runs.

The user path this change must make work:

1. **Server start.** Install and join a runner to a pool, then start the runner binary against the server.
2. **API.** Poll the NEW route `POST /runner/v1/claims` for work with the runner key; run `opendustpan status` on failure.
3. **API.** Heartbeat the claim every 15 s to renew the 60 s lease during work.
4. **API.** Stream the sweep report under the claim within the size bounds.
5. **Docs page.** Read the runner claims docs page and run its poll loop as written.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Poll with a bad runner key fails. | Server answers 401 with the NEW error code `runner.invalid`, with no key echo in body or logs. Check key and pool, then run `opendustpan status`. | `opendustpan status` |
| Poll with a retired runner fails. | Server answers 409 with `runner.retired` and names the retire record. Join a new runner, then run `opendustpan status`. | `opendustpan status` |
| Upload over the bound fails. | Server answers 409 with `runner.limit` when the report exceeds 1 GiB, 5000000 records, or a line exceeds 64 KiB. Fix the report, then run `opendustpan status`. | `opendustpan status` |

Evidence for the claims above:

- Claimed-operation lease behavior exists on main. Proof: pkg/durable/runner_claimed_test.go:16
- Runner invalid error code exists on main. Proof: internal/errcode/catalog_runner.go:8
- Lease renew by holder exists on main. Proof: pkg/audit/audittest/feed.go:391 (func testLeaseRenewByHolder)
- Runner protocol base route exists on main. Proof: route table or API description
- Runner protocol package exists on main. Proof: pkg/runner/protocol

## What Changes

- Add the NEW route `POST /runner/v1/claims` for poll with `ClaimWork` semantics.
- Add the NEW route `POST /runner/v1/claims/heartbeat` that renews the claim lease by 60 s with fence check.
- Add the NEW route `POST /runner/v1/uploads` for bounded report upload under the claim to the blob store encrypted with the data key of the namespace.
- Ship the NEW commands `opendustpan runner poll` and `opendustpan runner upload` with the runner poll and heartbeat loop in the runner binary.

Out of scope:

- Launcher step start and step validation
- Source filter enforcement
- Runner pool and join token management
- Sweep target lifecycle and finding triage

Depends on:

- `130-138-runner-pools-management` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Runner operator must learn two new paths at once: poll and heartbeat; the docs page shows one copy-paste loop.
- Empty poll answer can look like a fault; the answer carries an explicit retry delay.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Lease renew is checked by holder on main. (`pkg/audit/audittest/feed.go:391 (func testLeaseRenewByHolder)`)
- Server start steps exist on main. (`pkg/dustpan/start.go:66 (func Server.startSteps)`)
- Source filter option exists on main. (`pkg/dustpan/sourcefilter.go:15 (func WithSourceFilters)`)
- Postgres source filters read exists on main. (`pkg/store/postgres/sourcefilter.go:21 (func Store.SourceFilters)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current), 12 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "A sweep is not a job. A job belongs to one unit, and the one-change rule, the phases and the gates of ADR 100 serve changes to a unit. A sweep changes nothing and has no unit. A sweep uses the same parts as a job: the runner pools, the ru"
- Quote: "The durable operation. The start of a sweep starts one durable operation of the kind `unmanaged.sweep.<pool ID>`, with the start key `<sweep ID>`, in the transaction that stores the sweep. A runner of the pool claims it through the runner"
- Quote: "Upload. The runner streams the report to the server through the runner protocol, under its claim. The server writes it to the blob store as a stream (ADR 45), encrypted with the data key of the namespace, because tags can hold names and t"
- Effect: Claim polling reuses runner pools and runner claims for sweeps and jobs. Report upload streams under the claim to the blob store encrypted with the data key of the namespace.
- Conflict: none.

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

- Do claims need a lease with renew and fence? Yes. RFC 6 states claims are leases, the runner renews the lease while it works, expiry returns the work to another runner, and the claim is a write in the namespace through the fence. The change follows that rule with a 60 s renew, a 15 s heartbeat, and a 50 s long-poll timeout decided in this change.
- Does polling need a push channel? No new tool. The runner opens every connection and long-polls the server per RFC 6. The change adds no push channel.
- Where do report bounds come from? ADR 164 decisions 10 to 12 answer it: JSON Lines version 1, at most 1 GiB and 5000000 records, 64 KiB per line, streamed under the claim to the blob store encrypted with the data key of the namespace. The change enforces those bounds.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
