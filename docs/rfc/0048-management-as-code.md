---
rfc: 48
title: Managing OpenDustpan as code
status: draft
created: 2026-10-10
---

# RFC 48. Managing OpenDustpan as code

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Platform teams manage their backend with IaC: the `tfe` provider for HCP
Terraform, the Pulumi Service Provider for Pulumi Cloud. Spacelift has
`spacectl`, env zero and Harness have CLIs.

## First ideas

- The TFE-compatible API may let the `tfe` provider manage workspaces; check
  which resources work.
- A provider of our own for namespaces, teams, profiles, policies and
  blueprints.
- An admin CLI, as a role of the one binary (RFC 7).

## Open questions

- Which first: a provider or a CLI?

## Outcome

None yet.
