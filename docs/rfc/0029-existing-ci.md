---
rfc: 29
title: Using an existing CI as the executor
status: accepted
created: 2026-10-10
adr: 54, 100, 101, 152
---

# RFC 29. Using an existing CI as the executor

Status: accepted (ADRs 54, 100, 101 and 152). It was a stub opened from the
competitor review in the research doc "IaC and TACOS landscape".

## Problem

Many teams will not run a new agent. Terramate and Gruntwork run entirely in
the customer's GitHub Actions or GitLab CI and report results; Atmos Pro
dispatches GitHub Actions; OpenTaco runs inside existing CI. RFC 6 has pull
runners only.

## First ideas

- A reporting API: a CI job reports a preview, a deployment or a drift result
  for a unit, with its logs and commit.
- Optional dispatch: OpenDustpan starts a CI workflow instead of a runner job,
  through the forge connection (RFC 12).
- CI signs in with OIDC token exchange (RFC 20).

## Open questions

- Is a CI job just another runner kind in RFC 6, or a separate path?
- How much of the job model (leases, labels) applies to work that OpenDustpan
  does not schedule?

## Outcome

Accepted on 2026-10-18 through these ADRs:

- ADR 100: an existing CI is not a kind of runner. Write-back is the path
  for a team that applies in its own CI, and OpenDustpan schedules no work
  in that CI.
- ADR 101: the write-back executor, and the verification of the state
  version that the CI writes.
- ADR 54: a CI job authenticates with workload identity (OIDC or JWT token
  exchange), not with a static token.
- ADR 152: the history policy of the plans and logs of a job.

A question of this RFC that these ADRs do not decide needs a new RFC.
