---
rfc: 20
title: Access tokens and machine identity
status: draft
created: 2026-10-10
---

# RFC 20. Access tokens and machine identity

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

CI is the main client of a state backend. Pulumi Cloud lets CI log in with an
OIDC token from GitHub Actions and similar, and has organization and team
tokens. HCP Terraform has a maximum token lifetime and expiry emails. Scalr
reports unused tokens. env zero gives AI agents a read-only sign-in. ADR 6
says OpenDustpan issues tokens but sets no kinds, lifetimes or reports.

## First ideas

- Token kinds: personal, team, namespace, runner (RFC 6), job (bound to one
  unit, RFC 15).
- Expiry, revoke, last-used time, a namespace maximum lifetime, and a report
  of unused tokens.
- OIDC token exchange: CI presents its provider's token and gets a short-lived
  OpenDustpan token, with trust rules per namespace.
- Read-only tokens, for dashboards and agents (RFC 41).

## Open questions

- Does the Pulumi CLI's OIDC login flow work against a self-hosted backend?
  This needs a check with `make e2e`.
- Which TFE token endpoints (organization, team) must the TFE-compatible API
  serve?

## Outcome

None yet.
