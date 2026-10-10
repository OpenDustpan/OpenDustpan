---
rfc: 33
title: Customer-managed keys
status: draft
created: 2026-10-10
---

# RFC 33. Customer-managed keys

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Security-sensitive buyers want their own key management service to hold the
key-encryption key. Pulumi Cloud (Pro) and HCP Terraform (Premium) offer this.
RFC 3 makes the key wrapper a port, with only a local key planned.

## First ideas

- Wrapper adapters for AWS KMS, Google Cloud KMS, Azure Key Vault and
  HashiCorp Vault Transit.
- Per-namespace keys, so that one customer can revoke access to its namespace.
- Decide whether blobs (state, plans) are also encrypted at rest by
  OpenDustpan, or left to the storage layer.

## Open questions

- Is one key per namespace enough, or per unit?

## Outcome

None yet.
