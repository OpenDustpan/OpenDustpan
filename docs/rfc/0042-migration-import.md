---
rfc: 42
title: Migration from other backends and operators
status: accepted
adr: 29
created: 2026-10-10
---

# RFC 42. Migration from other backends and operators

Status: accepted (ADR 29) for milestone 3: an import as a tested guide, for
four first sources. The import resource, the source adapters and runner
execution stay open. ADR 29 says "import" and "source" for the move of a unit
into OpenDustpan, because "migration" means a SQL schema migration here.

The rest stays a draft. The scope and the order of sources are proposals. Migration is
planned early: every team that tries OpenDustpan already has state somewhere
else, and a migration that loses history or secrets ends the trial.

## Problem

Teams arrive with state, history, config and secrets in another product:
Pulumi Cloud, Pulumi's bucket backends, HCP Terraform, Terraform Enterprise,
OpenTofu backends (S3, GCS, Azure, PostgreSQL, HTTP), Spacelift, env zero,
Scalr, OTF, Terrakube, OpenTaco, and the Kubernetes operators (RFC 31). env
zero has a migration wizard from HCP Terraform. Nobody offers a careful,
tool-native migration into a self-hosted backend for both tools.

## What can move

| Item | Notes |
| --- | --- |
| Current state | Required. Checked after the move (resource count, serial or version, lineage). |
| State history | Pulumi checkpoints and OpenTofu state versions, where the source keeps them. Optional, with a date limit. |
| Config and variables | Pulumi stack config, TFE workspace variables and variable sets, into profiles or unit config (RFC 5, RFC 12). |
| Secrets | The hardest part. See below. |
| Tags, descriptions, settings | Mapped to labels (RFC 4) and unit settings. |
| Access | Teams and permissions, mapped to roles (RFC 19) as a proposal that an admin accepts. Never applied silently. |
| Run and update history | Logs and events, if the source exposes them. Lowest priority. |
| Policies, schedules, triggers | Reported as "not moved, do this by hand" at first. |

## Secrets

- **Pulumi stacks** that use Pulumi Cloud's service secrets provider hold
  ciphertext that only Pulumi Cloud can decrypt. The migration must decrypt
  through the source (for example with the CLI's own change of secrets
  provider) and re-encrypt under OpenDustpan's `secret` package, in one step,
  with no plain text written to disk.
- **Passphrase and cloud-KMS providers** may move as they are, or be
  converted.
- **OpenTofu state** holds sensitive values in plain text; the migration moves
  it as it is, and warns when the source used client-side state encryption.

## Safety rules

- **Dry run first:** a report of what moves, what does not, and why.
- **Lock during the move:** lock the unit in the source where the source
  allows it, so that no run writes there during the move.
- **Verify, then switch:** compare the moved state with the source before the
  team points its CLI or runners at OpenDustpan.
- **Idempotent and resumable:** a failed move can run again without
  duplicates.
- **Never delete in the source.** The team decides when to remove the old copy.
- **Rollback:** until the switch, the source is untouched and still the record.

## Shape

- A migration is an API resource (ADR 17), with a dry run, a run, a report and
  an audit trail (RFC 22). The console and a CLI use the same API.
- Each source is an adapter behind a port, with a conformance suite (ADR 2), so
  the community can add sources.
- Migration from the Kubernetes operators converts their resources to ours
  (RFC 31). It never accepts another project's resource types as ours.

## Order of sources (proposal)

1. Pulumi bucket backends and Pulumi Cloud (the gap that only OpenDustpan
   fills).
2. OpenTofu standard backends (S3, GCS, Azure, HTTP) and HCP Terraform / TFE.
3. Pulumi Kubernetes Operator, tofu-controller, Burrito.
4. The other TACOS, by demand.

## Open questions

- Does the migration run in the server, in a runner (it may need source
  credentials and the tools' CLIs), or in a CLI on a workstation?
- How much history is worth moving by default?
- Can Pulumi Cloud ciphertext be re-encrypted without the stack's program,
  only with the CLI and the source's API? This needs a check with `make e2e`.
- Which sources can be locked during a move, and what does the migration do
  for those that cannot?

## Outcome

Accepted in part as [ADR 29](../adr/0029-importing-existing-state.md) for
milestone 3: an import as a tested guide, for four first sources. The import
resource, the source adapters and runner execution stay open for a later
decision.
