---
rfc: 17
title: Stack and workspace lifecycle: delete, rename, move, tags
status: draft
created: 2026-10-10
---

# RFC 17. Stack and workspace lifecycle: delete, rename, move, tags

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

The Pulumi CLI calls endpoints that OpenDustpan does not serve: `pulumi stack
rm` (with `--force`), `pulumi stack rename`, `pulumi stack tag`, and stack
transfer between organizations. The TFE-compatible API has workspace delete,
rename and tags. Pulumi Cloud and HCP Terraform have these at their free tier.
HCP Terraform cannot undo a workspace delete.

## First ideas

- Map each CLI command to its endpoint and to a tool-neutral action (ADR 13
  already names rename and delete).
- Soft delete: keep a deleted unit's state for a set time, with restore. This
  would be better than HCP Terraform.
- Destroy on delete (tofu-controller, Pulumi Kubernetes Operator): an opt-in
  job (RFC 6) that destroys resources before the record goes, with a
  confirmation.
- Tags and labels depend on RFC 4. Pulumi stack tags and TFE workspace tags
  may both map to the same labels.

## Open questions

- Answered by ADR 19 for stacks: a rename keeps the stable ID, so the scope
  (secrets) and the references (RFC 15) survive.
- How long does soft delete keep state, and who can restore?
- Is moving a unit between namespaces allowed, given home regions (ADR 4)?

## Outcome

None yet.
