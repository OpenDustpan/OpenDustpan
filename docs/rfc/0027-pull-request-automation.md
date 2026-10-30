---
rfc: 27
title: Pull request automation
status: draft
created: 2026-10-10
---

# RFC 27. Pull request automation

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

RFC 12 gives forge connections, webhooks, statuses and merges. Atlantis,
Stategraph, OpenTaco, env zero, Scalr, Terramate, Gruntwork and Pulumi review
stacks give more in the review request itself, and this is how most OpenTofu
teams work today.

## First ideas

- One summary comment edited in place, with a table of `+ ~ -` per unit and
  links to the console; GitHub Checks as an alternative.
- Comment commands (`plan`, `apply`, `unlock`) with limits on who may use
  them.
- A stale-plan warning with the command that fixes it, and conflict detection
  after a deployment (Terramate).
- Apply requirements: approved, mergeable, not behind the base branch, code
  owners.
- Locks that a review request holds from plan until merge or close, for
  OpenTofu workspaces too (RFC 12 has a hold for Pulumi only).
- A temporary unit per review request (Pulumi review stacks), destroyed on
  close (RFC 43).
- A global switch that stops applies, for incidents and change freezes
  (Atlantis).

## Open questions

- Which of these need a runner, and which can the backend do from webhooks
  alone?
- How does comment noise stay low in a monorepo with many units?

## Owner direction (2026-10-30)

The project owner gave this direction on 2026-10-30, through the project
manager. This section decides nothing. An ADR records a decision (ADR 15).

- The first slice for a pull request is a plan, one commit status and one
  summary comment.
- An apply runs after the merge, from the default branch, by default.
- A pre-merge apply, from the branch of the pull request, is also an
  option. It is not the default, and the docs say that the project does not
  recommend it.
- Runners run both tools. The gates and approvals of ADRs 222 to 230 are
  the policy checks.

## Outcome

None yet.
