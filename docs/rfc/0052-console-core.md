---
rfc: 52
title: Console core views and navigation
status: draft
created: 2026-10-10
---

# RFC 52. Console core views and navigation

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

The research doc lists the screens every mature product converged on.
OpenDustpan's console has early views only.

## First ideas

- Unit list with status buckets and counts, filters in the URL, saved views
  (RFC 40).
- Update and run page with a timeline, policy results as events, and a
  per-resource diff next to the raw log.
- A "connect your CLI" snippet per unit with the exact `pulumi login` or
  `cloud` block (Scalr).
- A command palette; sections the user cannot use stay visible with the reason
  and what grants access (the Pulumi complaint).
- Extension points for scanner and cost output (Terrakube), without running
  stored code in the browser.

## Open questions

- Which of these is the first milestone of the console?
  - Spike all of them and iterate from there until the fog is clear and we have enough information to make a decision.
    I'm not sure about `Extension points for scanner and cost output (Terrakube), without running
  stored code in the browser.`. This is too tool specific, and we should probably do a deeper dive in a separate RFC
    first for integrating spend/cost OPS tools.

## Outcome

None yet.
