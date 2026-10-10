---
rfc: 56
title: Upgrade pull requests for providers, modules and blueprints
status: draft
created: 2026-10-10
---

# RFC 56. Upgrade pull requests for providers, modules and blueprints

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Gruntwork Patcher opens pull requests that upgrade module and provider
versions and promotes them across environments. HCP Terraform and Pulumi show
which units use an old version.

## First ideas

- Use the forge connection (RFC 12) to open upgrade review requests for units
  that pin old versions.
- Roll out across units in order (RFC 24).

## Open questions

- Is this a feature of the registry (RFC 10), of blueprints (RFC 14), or
  separate?

## Outcome

None yet.
