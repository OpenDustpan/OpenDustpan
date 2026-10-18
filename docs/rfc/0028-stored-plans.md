---
rfc: 28
title: Stored plans and plan-bound approvals
status: accepted
created: 2026-10-10
adr: 102, 152, 191, 194, 213
---

# RFC 28. Stored plans and plan-bound approvals

Status: accepted (ADRs 102, 152, 191, 194 and 213). It was a stub opened from
the competitor review in the research doc "IaC and TACOS landscape".

## Problem

A reviewer must see what will change, and the apply must be the change that
was approved. tofu-controller approves a plan by its ID; Spacelift, env zero,
Scalr and Harness show the plan at the approval step; env zero downloads it as
JSON. RFC 6 keeps job logs only, and RFC 13's approvals are not bound to a
plan.

## First ideas

- Store each plan or preview as a blob (the blob port), with a hash, retention
  and access rules.
- Bind an approval to the plan hash; refuse an apply when the plan changed.
- Auto-approve a plan with no changes; approval timeout; named approvers
  notified through RFC 23.
- A structured diff in the console from the plan's JSON, with sensitive values
  masked at the source.

## Open questions

- A plan is not state, but it holds values that can be secret. Is a stored
  plan covered by the glossary rule on state, or by a rule of its own?
- For Pulumi, is a saved update plan (`--save-plan`) stable enough to bind to?

## Outcome

Answered in part by ADR 102 (job plans and logs) and ADR 152 (history policy
for job artifacts), on 2026-10-17.

ADR 191 (plan revisions, unit plans and signatures) binds the approvals of
a change order to the digest of all its stored plans, on 2026-10-18.

ADR 194 (the review view of a big plan) shows a stored plan to a reviewer,
on 2026-10-18.

ADR 213 decision 2: a plan with no change ends `succeeded` with no apply and
needs no signature, on 2026-10-18, as the project manager decided under the owner's decision rule.

Closed on 2026-10-18. A question of this RFC that these ADRs do not decide
needs a new RFC.
