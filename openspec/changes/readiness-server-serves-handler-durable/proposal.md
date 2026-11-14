---
version: 1
---

Issue: #2463.

## Problem

An Embedder builds an api-only server with a durable store, serves Handler() without Run, and the server reports not ready after 60 s with failed claim-loop, which hides the real state and delays diagnosis.

## Evidence

- The worker watch file exists on main. Proof: Path pkg/dustpan/workerwatch.go exists per pack paths.
- Main has the Handler method, the durable store option, the claimLoopRan method, and readiness tests. Proof: pkg/dustpan/dustpan.go:1958 (func Server.Handler); pkg/dustpan/schedules.go:32 (func WithDurableStore); pkg/dustpan/workerwatch.go:201 (func workerWatch.claimLoopRan); pkg/dustpan/health_test.go:193 (TestHealthReadyOK); pkg/dustpan/components_internal_test.go:214 (TestClaimLoopRan).

## Fix boundary

- Gate the claim-loop readiness check on the worker component so api-only servers stay ready.
- Keep the claim-loop check for worker and full servers with a durable store, with startup grace before 2 intervals.

Out of scope:

- Change of claim-loop timing or intervals.
- Change of e2e harness component layout.
- New health endpoints.

## Check

- Add failing readiness tests with a fake clock for api-only, worker-only, and full servers in pkg/dustpan. Check: NEW test TestApiOnlyWithDurableStoreStaysReady in pkg/dustpan fails on main with 503 claim-loop after 60 s fake time.
- Gate the claim-loop check on the workerEnabled flag in pkg/dustpan. Check: NEW tests pass, TestClaimLoopRan still passes, and a mutation test that removes the workerEnabled guard fails.
- Wire the gated check into the Start, Run and readiness path of the server. Check: `curl -s http://localhost:8080/health/ready` answers 200 for api-only and healthy worker servers on route GET /health/ready, and 503 only for worker and full layouts with a stuck loop.
- Document the component rule for embedders on the existing SDK docs page. Check: The docs page shows api-only, worker-only and full layouts with the readiness rule.
- Run full verification Confirmed by: CI (make check) Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Server exposes an HTTP handler constructor. (`pkg/dustpan/dustpan.go:1958 (func Server.Handler)`)
- Server supports a durable store option. (`pkg/dustpan/schedules.go:32 (func WithDurableStore)`)
- Worker watch tracks whether the claim loop ran. (`pkg/dustpan/workerwatch.go:201 (func workerWatch.claimLoopRan)`)
- Readiness happy-path test exists. (`pkg/dustpan/health_test.go:193 (func TestHealthReadyOK)`)
- Claim-loop test exists. (`pkg/dustpan/components_internal_test.go:214 (func TestClaimLoopRan)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Must api-only readiness skip the claim loop? Yes. The issue text decides it: an api-only server must not check the claim loop; worker and full servers with a durable store must still fail readiness when the loop does not run.
- Is api-only with a durable store refused at start? No. Api-only with a durable store is valid because the gated check makes it ready, so no start refusal is added.
- Must full servers and no-store servers keep defined readiness? Yes. A full server runs the worker component so the worker rule covers it. Servers with no durable store keep the old pass rule.
- What does readiness report before the first claim cycle and when Run was never called? While less than 2 claim intervals passed since Start the claim-loop failure stays excluded. After that a worker server that never ran the loop fails.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
