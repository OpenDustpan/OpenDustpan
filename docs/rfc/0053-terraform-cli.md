---
rfc: 53
title: Terraform CLI support
status: draft
created: 2026-10-10
---

# RFC 53. Terraform CLI support

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Many teams still run the Terraform CLI. Every TFE alternative (OTF, Terrakube,
OpenTaco, Scalr) and every PR tool supports it. ADR 12 supports a tool only
once the e2e suite drives its real CLI, and RFC 1 tests OpenTofu only.

## First ideas

- Add the Terraform CLI to the e2e suite over the same TFE-compatible API.
- Note the license (BUSL) for test use, and say in the docs which versions are
  tested.

## Open questions

- Is Terraform a separate tool in ADR 12's sense, or the same adapter with a
  second CLI?

## Outcome

None yet.
