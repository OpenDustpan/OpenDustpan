---
rfc: 42
title: Migration from other backends
status: draft
created: 2026-10-10
---

# RFC 42. Migration from other backends

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

env zero has a migration wizard from HCP Terraform. Teams arrive with state in
HCP Terraform, Terraform Enterprise, Pulumi Cloud, S3 buckets or other
self-managed backends.

## First ideas

- Import workspaces, variables and state versions from HCP Terraform and TFE
  through their API.
- Import stacks with history from Pulumi Cloud and from Pulumi's bucket
  backends.
- A dry run that lists what will move and what will not.

## Open questions

- Is history imported, or only the latest state?

## Outcome

None yet.
