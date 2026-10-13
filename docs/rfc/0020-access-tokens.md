---
rfc: 20
title: Access tokens and machine identity
status: discussing
created: 2026-10-10
adr: 53, 54
---

# RFC 20. Access tokens and machine identity

Status: discussing. Opened as a stub from the competitor review in the
research doc "IaC and TACOS landscape". The project owner chose
the token life cycle below on 2026-10-11.

The token life cycle is accepted as ADR 53 and workload identity as ADR 54 on
2026-10-13. The questions in "Open questions" stay open.

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

Each "lead proposal" is a proposed default from the project lead for the
owner to decide. It is not a decision.

- (e2e) Does the Pulumi CLI's OIDC login flow work against a self-hosted
  backend?
- (e2e) Which TFE token endpoints (organization, team) must the TFE-compatible
  API serve?
- Which roles may rotate or revoke the access token of another principal?
- Do the maximum lifetime and the inactivity period belong to the
  installation, to a namespace, or to both? Lead proposal: an installation
  setting that a namespace can make stricter, not looser.
- What is the default maximum lifetime, and how long before the expiry do
  the warnings start? No standard sets either number. Lead proposal: a
  maximum of 1 year, and warnings 14 days and 3 days before the expiry.
- (e2e) Which CLI shows the expiry warning: the `opendustpan` CLI, or the
  tool CLIs? A tool CLI shows a server warning only if its protocol has a
  place for one. Lead proposal: the `opendustpan` CLI always. A tool CLI
  only where `make e2e` shows that the tool prints the warning.
- Where do the SMTP settings live, and are they run-time settings (ADR 17)?
- (e2e) Which tool CLIs can send a TLS client certificate? If a tool cannot,
  can a workload use mTLS at the exchange endpoint to get an access token?
  Lead proposal: yes, the exchange endpoint accepts mTLS as well as a JWT.
- How does mTLS work when a proxy of the operator ends TLS before the
  server? Lead proposal: the proxy sends the checked client certificate in
  a header. The server accepts that header only from proxies that the
  operator names, as for trusted-header sign-in (ADR 55).
- What is the lifetime of an exchanged access token, and its default? Lead
  proposal: 1 hour by default, and an administrator can change it.
- Can an administrator turn off static service tokens for a namespace?
  Lead proposal: yes. Service tokens stay on by default.

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

## Owner direction (2026-10-13)

The project owner answered questions in this RFC on 2026-10-13. Words in
quotation marks are the words of the owner.

- Question "Workload identity in milestone 4": "OIDC/JWT and mTLS in M4".
  Milestone 4 has token exchange for any OIDC or JWT issuer, with trust
  rules per namespace. It also has mTLS client certificates, SPIFFE or
  other. Static service tokens are discouraged for machines.
- Question "What inactivity default does the server ship with, within the
  limit of 90 days or fewer?": "What do ISO/SOC2/PCIDSS recommend? Follow that
  as the default but admins can override this to any value including
  infinite." This replaces the limit "90 days or fewer" of the token life
  cycle above. The default follows the standards. An administrator can set
  any value, also no limit. The research doc `docs/research/m4-identity-defaults.md`
  holds the findings.
- Question "How does the server warn a principal before its access token
  expires?": "Event/Webhook for this goes to M5., API field + console banner,
  Email, CLI warning". Milestone 4 has an API field, a console banner, an
  email and a CLI warning. The email needs SMTP settings. The event and the
  webhook go to milestone 5 (RFC 23).

Second round, through the project lead, on 2026-10-13:

- Questions "Does the 90-day inactivity disable also apply to user
  accounts?" and "Should the bootstrap and break-glass tokens be exempt from
  the inactivity disable?": "Configurable. Default to tokens + accounts;
  exempt break-glass, but allow all of these settings." So by default the
  90-day disable covers access tokens and user accounts, and the break-glass
  credential is exempt. Each of these is a setting.

## Outcome

Accepted in part on 2026-10-13:

- ADR 53, the token life cycle:
  - a last-used time and an expiry on each access token, and a maximum
    lifetime that an administrator sets;
  - an inactivity disable of access tokens and user accounts after 90 days
    by default, with the break-glass credential exempt. The number comes
    from PCI DSS v4.0.1 requirement 8.2.6, which names user accounts. No
    standard sets a number for tokens. An administrator can change or turn
    off each part;
  - service tokens, rotation, and the prompt to retire the bootstrap admin
    token;
  - expiry warnings as an API field, a console banner, an email and a CLI
    warning. Events and webhooks for expiry go to milestone 5.
- ADR 54, workload identity: token exchange for any OIDC or JWT issuer and
  mTLS client certificates, with trust rules per namespace. Static tokens
  are discouraged for machines.

The questions in "Open questions" have no outcome yet.
