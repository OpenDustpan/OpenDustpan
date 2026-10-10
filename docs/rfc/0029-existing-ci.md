---
rfc: 29
title: Using an existing CI as the executor
status: draft
created: 2026-10-10
---

# RFC 29. Using an existing CI as the executor

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

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

None yet.
