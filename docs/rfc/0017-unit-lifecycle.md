---
rfc: 17
title: Stack and workspace lifecycle: delete, rename, move, tags
status: draft
created: 2026-10-10
---

# RFC 17. Stack and workspace lifecycle: delete, rename, move, tags

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

The part on soft delete and on the move of a unit between namespaces is
accepted as ADR 47. The rest stays a draft.

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

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Question "How long does soft delete keep state, and who can restore?":
  "Configurable, default to 30 days or something reasonable." And: "As with
  most questions - `who can` is usually answered with \"a user with permission
  by our FGA model\"."
- Question "Is moving a unit between namespaces allowed, given home regions
  (ADR 4)?": "No. Namespaces can obviously be moved between instances for
  failover or DR, but they own their contents."
  - The owner says that users can export their units and import them again
    where they want. The owner says that a unit is too bound to its namespace
    for an automated move. The owner asks: "What happens to their access
    grants in the target namespace?" and "What happens to their bound profiles
    in the target namespace?"
  - "I think a migration guide and at a later stage CLI tooling might be a
    better fit here, with checks for things that will inevitably break
    import/export." The owner says that the scope of this tooling is not clear
    yet. The owner asks for a separate RFC for it.

## Outcome

The part on soft delete and on the move of a unit between namespaces is
accepted as ADR 47 on 2026-10-12. The rest has no outcome yet.

ADR 47 follow-up, decided by the project lead under the owner's delegation on
2026-10-13: a delete frees the name at once, and a restore needs a free name
(409 when it is taken, or a new name in the request). `pulumi stack rm
--force` is the same soft delete, and a separate `unit:purge` action removes a
deleted unit before its retention period ends.
