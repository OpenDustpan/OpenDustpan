---
rfc: 24
title: Run triggers and orchestration between units
status: draft
created: 2026-10-10
---

# RFC 24. Run triggers and orchestration between units

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

RFC 6 says triggers come "later, one at a time". Competitors trigger runs from
schedules, git pushes filtered by path, git tags, inbound webhooks and
upstream changes. HCP Terraform has run triggers between workspaces; Spacelift
starts a dependent stack only when a referenced output changed and cancels
everything below a failure; env zero runs a workflow graph with "run from
here"; Terramate and Atmos Pro run only the units that a change affects.

## First ideas

- Trigger kinds: schedule, git push with path filters, git tag, inbound
  webhook, upstream unit finished, upstream output changed (RFC 15).
- Affected-unit detection: map changed paths in a commit to the units whose
  source (RFC 12) contains them.
- Ordering: run units in dependency order, stop below a failure, and allow
  "run from here".
- Serve TFE `run-triggers` for OpenTofu.

## Open questions

- Is the dependency order taken only from RFC 15's output edges, or also
  declared?
- How are cycles reported?

## Outcome

None yet.
