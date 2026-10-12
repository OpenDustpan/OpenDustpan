---
rfc: 20
title: Access tokens and machine identity
status: discussing
created: 2026-10-10
---

# RFC 20. Access tokens and machine identity

Status: discussing. Opened as a stub from the competitor review in the
research doc "IaC and TACOS landscape". The project owner chose
the token life cycle below on 2026-10-11.

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
- The token life cycle in the Proposal below: last-used time and expiry on
  each access token, automatic disable after inactivity, service tokens,
  rotation, and a first-run prompt for the bootstrap admin token.

## Proposal

### Token life cycle

The project owner chose this life cycle. It covers each access token.

- Each access token records its last-used time and has an expiry.
- The server disables an access token automatically after a configurable
  inactivity period. The default and the maximum are 90 days or fewer.
- Service tokens serve machines that cannot sign in to the console. A service
  token belongs to a principal that is not a person.
- Rotation is a call that issues a new access token and sets an end time for
  the old one. A principal may keep using the old one until its end time.
- On the first sign-in with the bootstrap admin token (ADR 6), the console
  and the API ask the operator to make a personal admin token and to retire
  the bootstrap token. It helps with ISO/IEC 27002:2022 5.17.
- Every token operation has a REST API and an action (ADR 17). Each operation
  writes an audit entry (ADR 41).
- A CLI access token of a person is created after a sign-in. It has an
  expiry. See RFC 21 for the sign-in step.

## Open questions

- Does the Pulumi CLI's OIDC login flow work against a self-hosted backend?
  This needs a check with `make e2e`.
- Which TFE token endpoints (organization, team) must the TFE-compatible API
  serve?
- What inactivity default does the server ship with, within the limit of 90
  days or fewer?
- Which roles may rotate or revoke the access token of another principal?
- How does the server warn a principal before its access token expires?

## Owner direction (2026-10-11)

The project owner chose the token life cycle above. Each access token records
its last-used time and has an expiry. The server disables an access token
automatically after a configurable inactivity period. The default and the
maximum are 90 days or fewer. Service tokens serve machines that cannot sign
in to the console. Rotation is a call that issues a new access token and sets
an end time for the old one. On the first sign-in with the bootstrap admin
token, the console and the API ask the operator to make a personal admin token
and to retire the bootstrap token (ISO/IEC 27002:2022 5.17). Every token
operation has a REST API and an action (ADR 17), and writes an audit entry
(ADR 41).
The related ADRs are ADR 6, ADR 13, ADR 17 and ADR 41.

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Comment, with no question named: "We should also support arbitrary OIDC and
  JWT issuers and mTLS certificates (SPIFFE or otherwise) and discourage the
  use of static tokens for machines and workloads."
- Comment, with no question named: "The maximum token lifetime and which ones
  users can issue must be configurable by the admin (I guess the latter just
  becomes part of the FGA model)"

## Outcome

None yet.
