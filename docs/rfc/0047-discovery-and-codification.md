---
rfc: 47
title: Discovery and codification of unmanaged resources
status: draft
created: 2026-10-10
---

# RFC 47. Discovery and codification of unmanaged resources

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Firefly, Pulumi Insights and HCP Terraform Search find cloud resources that no
IaC manages and generate code with import blocks. ControlMonkey did the same.

## First ideas

- A runner job (RFC 6) that scans an account with credentials from a profile
  (RFC 5) and writes code as a review request (RFC 12).
- The backend never scans clouds itself.

## Open questions

- Is this in scope for a backend, or a separate tool that uses OpenDustpan's
  API?

## Outcome

None yet.
