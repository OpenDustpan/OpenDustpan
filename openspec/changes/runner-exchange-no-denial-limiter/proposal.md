---
version: 1
---

Issue: #2591.

## Problem

A flood of forged exchanges that pass the proof check writes one audit entry each, and refusals for a bad proof write no audit entry. The operator cannot bound the flood and cannot see the bad-proof attempts.

## Evidence

- Main already has a denial limiter constructor. Proof: pkg/dustpan/dustpan.go:767 (func defaultDenialLimiter)
- Main already has the refusal codes for invalid and limit. Proof: internal/errcode/catalog_runner.go:10 runner.invalid and internal/errcode/catalog_runner.go:13 runner.limit
- Main serves the trust rule exchange route. Proof: ANY/POST /api/dustpan/v1/namespaces/{}/trust-rules/exchange in the route table

## Fix boundary

- Add a per-source limit of 30 post-verification denials per 60 seconds on the exchange.
- Write one audit entry with a fixed reason class for each bad-proof refusal. Never write the proof.
- Write one summary audit entry per source per 60 seconds for suppressed denials, and count them in a metric.

Out of scope:

- Changes to proof verification logic.
- Changes to other runner routes.
- Changes to audit retention.

## Check

- Write the failing test for the bad-proof audit entry in NEW pkg/runner/exchange_denial_test.go. Confirmed by: CI (make check). Check: TestExchangeBadProofWritesReasonClassOnly in NEW pkg/runner/exchange_denial_test.go fails on main. It calls the exchange route with a trust token and reads the audit entries route with an access token.
- Write the failing test for the denial limit in NEW pkg/runner/exchange_denial_test.go. Confirmed by: CI (make check). Check: TestExchangePassingProofFloodIsLimited in NEW pkg/runner/exchange_denial_test.go fails on main. It calls the exchange route with a trust token and reads the audit entries route with an access token.
- Record the nonce row cost against the limiter cost for the exchange denial path in NEW pkg/runner/exchange_denial_bench.txt. Confirmed by: CI (make check). Check: NEW file pkg/runner/exchange_denial_bench.txt exists with the nonce row cost and `make check` passes.
- Write the reason-class audit entry in NEW pkg/runner/exchange_denial.go. Confirmed by: CI (make check). Check: TestExchangeBadProofWritesReasonClassOnly passes for the handler in NEW pkg/runner/exchange_denial.go.
- Enforce the limit and the suppressed summary in NEW pkg/runner/exchange_denial.go. Confirmed by: CI (make check). Check: TestExchangePassingProofFloodIsLimited passes for the handler in NEW pkg/runner/exchange_denial.go.
- Add an e2e step for a forged exchange that calls ANY/POST /api/dustpan/v1/namespaces/{}/trust-rules/exchange with a trust token. Confirmed by: CI (e2e job). Check: The e2e step calls ANY/POST /api/dustpan/v1/namespaces/{}/trust-rules/exchange with a trust token and passes in CI. Confirmed by: CI (e2e job).
- Document the limit and the audit entry in NEW docs/reference/runner-exchange.md. Confirmed by: CI (make check). Check: The page gives 30, per 60 seconds, per client address, namespace and rule.
- Run the usability check of the refusal path. A person's step. Check: A person completes the path from the docs page alone. A person's step.

Depends on:

- `runner-protocol-every-refusal-bare` (order of the milestone review)
- `architecture-review-7-nov-no` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a denial limiter constructor used by the server. (`pkg/dustpan/dustpan.go:767 (func defaultDenialLimiter)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR govern trust exchange refusals or denial limiting? No. The pack lists no ADR for it, so no decision conflicts.
- What may a bad-proof audit entry record? A fixed reason class only. The issue text says never the proof.
- Is the per-source key, budget, window and state scope decided? Yes. The key is client address plus namespace plus rule. The budget is 30 denials per 60 seconds. State is in memory in each server process, capped at 10000 keys with oldest-first eviction. A valid success never counts.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
