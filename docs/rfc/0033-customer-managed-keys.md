---
rfc: 33
title: Customer-managed keys
status: accepted
adr: 217
created: 2026-10-10
---

# RFC 33. Customer-managed keys

Status: accepted (ADR 217) on 2026-10-18: a customer key for each namespace,
built now.

A stub opened from the competitor review in the research doc
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

Accepted as [ADR 217](../adr/0217-customer-managed-keys-for-each-namespace.md)
on 2026-10-18 (owner decision). The customer key wraps the namespace key of
one namespace, and the namespace key wraps the data keys and item keys. The
open question gets this answer: one customer key and one namespace key for
each namespace. Blobs stay sealed by OpenDustpan (ADR 41, ADR 151). Open
parts are in RFC 76.
