---
rfc: 49
title:
  Run tasks: external checks with callbacks
status: draft
created: 2026-10-10
---

# RFC 49. Run tasks: external checks with callbacks

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

HCP Terraform run tasks call an external service at a stage of a run and wait
for a signed callback with pass or fail, advisory or mandatory. RFC 6 hooks
run on the runner, and RFC 13 approvals have webhooks, but nothing waits for
an external service.

## First ideas

- A callback protocol: the backend sends a signed request with a short-lived
  token; the service answers pass, fail or a message.
- Stages: before plan, after plan, before apply; advisory or mandatory.

## Open questions

- Is this a kind of policy (RFC 13), or a kind of job step (RFC 6)?
  - This is a policy steps. As a rule, jobs do not run anything except IaC code (and their dependencies) and die. We
    should never shoehorn other tasks into job runners.

## Outcome

None yet.
