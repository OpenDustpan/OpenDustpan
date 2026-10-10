---
rfc: 21
title: Directory sync and sign-in hardening
status: draft
created: 2026-10-10
---

# RFC 21. Directory sync and sign-in hardening

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Large organizations remove access through their identity provider. Spacelift,
env zero, HCP Terraform and Pulumi Cloud have SCIM provisioning. HCP Terraform
and env zero have IP allowlists. Pulumi Cloud has passkeys. OTF accepts
identity from a trusted proxy (Google IAP). ADR 6 covers sign-in adapters
only.

## First ideas

- SCIM 2.0 for users and groups, feeding teams (RFC 19).
- Passkeys for the local sign-in adapter.
- Trusted-header sign-in behind an identity-aware proxy, as an Authenticator
  adapter.
- IP allowlists, though self-hosters can also do this at their own proxy.

## Open questions

- Which of these belong in the binary, and which are better left to the
  operator's proxy?

## Outcome

None yet.
