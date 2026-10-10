---
rfc: 54
title: Sources other than git
status: draft
created: 2026-10-10
---

# RFC 54. Sources other than git

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

The Pulumi Kubernetes Operator and tofu-controller take programs from OCI
artifacts, buckets and inline definitions, through Flux sources. RFC 12 covers
git forges only.

## First ideas

- OCI artifacts as a source, possibly stored in the RFC 10 registry.
- Bucket sources for air-gapped installs.

## Open questions

- Does write-back (RFC 12) make sense for a source that is not git?

## Outcome

None yet.
