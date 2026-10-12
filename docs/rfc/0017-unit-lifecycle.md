---
rfc: 17
title:
  Stack and workspace lifecycle: delete, rename, move, tags
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
  - Configurable, default to 30 days or something reasonable.
  - As with most questions - `who can` is usually answered with "a user with permission by our FGA model".
- Is moving a unit between namespaces allowed, given home regions (ADR 4)?
  - No. Namespaces can obviously be moved between instances for failover or DR, but they own their contents.
  - Users can obviously export their units and re-import them wherever they want, but units are too namespace bound and
    this creates too many other questions for automated migrations. "What happens to their access grants in the target
    namespace?". "What happens to their bound profiles in the target namespace?"
  - I think a migration guide and at a later stage CLI tooling might be a better fit here, with checks for things that
    will inevitably break import/export. The of this CLI tooling is unclear at this stage, create a separate RFC for
    that.

## Outcome

None yet.
