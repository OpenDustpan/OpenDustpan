---
rfc: 117
title: The first administrator of an installation with no person who can make a grant
status: discussing
created: 2026-11-02
---

# RFC 117. The first administrator of an installation with no person who can make a grant

Status: discussing. Issue #2276 (QA round 8) and issue #2196 (QA round 6)
show the problem. No accepted ADR decides how the first person of a
production installation gets a role, and the accepted ADRs that touch the
question pull in different directions. This RFC gives the facts, the
options, the risks and a recommendation. It builds nothing. The project
owner decides, and an ADR records the decision.

## Problem

On a new production installation (`opendustpan serve`), nobody can make the
first grant, the first gate or the first group change:

1. The operator starts `serve` with a bootstrap admin token and no identity
   provider. The bootstrap admin token makes a namespace and a local user.
   The person redeems the set-up code, sets a password and signs in to the
   console.
2. The person has no grant, so the person can do nothing.
3. The bootstrap admin token cannot give the person a role.
   `POST /api/server/grants` answers `403 mfa.fresh-step-required`. So do
   the grants on a namespace, a collection and a principal, the roles, the
   members of a group, the gate objects and the approval rules.
4. `opendustpan host create-temp-admin` makes a temporary administrator. It
   gives a token, not a console session, so it gets the same `403`.

Only `opendustpan dev` gives an administrator session, through its one-time
sign-in link. `serve` never serves that link.

The same gap blocks an installation with an OpenID provider: the first
sign-in of a person makes a principal with no grant, and the console sign-in
guide says "Give the first roles with the bootstrap admin token, through the
grants API". That sentence is wrong on `main`. Issue #2196 found the same
`403` in step 1 of the workload identity guide.

The rule that a change needs a fresh authentication stays. This RFC asks
only how the first person with a role comes to exist.

### Facts on `main` at 6c35afa

- `internal/api/server/freshness.go`: `fresh()` wraps each write that needs
  a fresh authentication. `requireFresh` answers `errFreshStepRequired` to
  each request that has no console session. The route test lists 22 such
  routes (`freshAuthRoutes` in `internal/api/server/routes_test.go`),
  among them every `POST` of a grant, `POST` and `PATCH` of a role, and
  the writes of the members of a group.
- `pkg/identity/identity.go`: `StaticToken` gives a principal with
  `Admin: true`, no `Kind` and no `Credential`. It is not a stored person
  and it has no session.
- `POST /api/dustpan/v1/local-users` needs `server:manage-sign-in` and no
  fresh authentication. The bootstrap admin token can call it. The redeem
  of a set-up code (`POST /api/dustpan/v1/local-users/redeem`) answers
  `204` and starts no session.
- A password sign-in of a person with no second factor, on an installation
  with no SSO provider and `mfa.requireForAll` off, is a fresh
  authentication (ADR 285 decision 21).
- `opendustpan dev` grants `site-administrator` to the person `dev:admin`
  through the access service, with the bootstrap administrator as the
  granter, at the sign-in of the one-time link (ADR 252 decision 2). That
  path exists only on a dev installation.

## What the accepted ADRs say

None of these decides how the first administrator of a production
installation is made. Quotes are from the ADR files on `main`.

| ADR | What it says | Effect on the question |
| --- | --- | --- |
| 6 | "The static token is for first boot. Real users, tokens and roles are the next identity work." | The bootstrap admin token is the tool of the first boot. It names no way to make the first person. |
| 39, Consequences | "The bootstrap admin token must work on an empty installation for the first namespace and for a restore." | Names the namespace and a restore, not a grant. |
| 53 decision 9 | "On the first sign-in with the bootstrap admin token (ADR 6), the console and the API ask the operator to make a personal admin token and to retire the bootstrap token." | Assumes that a person with a role exists. A token create also needs a fresh authentication (ADR 227 decision 2), so the bootstrap token cannot do it either. |
| 70 decision 24 | "The first setup cannot finish without two break-glass accounts." | A first setup exists as a concept. No ADR designs it. |
| 70 decision 27 | "The host token makes a temporary administrator for one named person." | Recovery, not first setup. |
| 212 decision 3 | "An access token acts with no MFA step (ADR 46 decision 8), so it can never pass this step." | The bootstrap admin token is an access token for this rule. |
| 227 decision 6 | "A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing. [...] An access token never gives a fresh authentication." | No exception for the bootstrap admin token. |
| 237 decision 12, 252 decision 2 | `dev` prints a one-time sign-in link, and "The bootstrap administrator of `dev` [...] gives the person the site-administrator role." "`serve` and an SDK server never serve it." | The only accepted path to a first administrator session. Dev only. |
| 245 decision 4 | "A temporary administrator cannot start a console session through an identity provider. [...] It uses only the token that the command printed." | The temporary administrator cannot pass a fresh step. |
| 245 decision 2 | "The command refuses when the installation chose mode 3 of ADR 57." | Host recovery is not always there. |
| 285 decision 5 | "An administrator who makes a local user receives a one-use set-up code." | A set-up code sets a credential. It carries no role. |
| 285 decision 21 | "The bootstrap admin token stays outside the setting (RFC 21 O12)." | About "require MFA" at sign-in, not about a fresh step. |
| 297 decision 20 | "A temporary administrator (ADR 245) still has no console session." | Confirms ADR 245 decision 4. |

Two conflicts:

- ADR 6 and ADR 39 make the bootstrap admin token the tool of the first
  boot. ADR 212 decision 3 and ADR 227 decision 6 refuse it for each change
  that needs a fresh authentication, and the first grant is such a change
  on `main`.
- ADR 53 decision 9 asks the operator to retire the bootstrap admin token
  after the first sign-in. With no person who can make a grant, the
  operator cannot retire it.

## Options

Each option keeps the fresh-step rule for every other change. Each
exception that an option adds is a stored rule with an audit entry, which a
person can list in the API.

### A. A first-administrator code from a host command

`opendustpan host first-admin --principal <id> --reason <text>` runs on the
server machine. It opens the database, as `host create-temp-admin` does
(ADR 245 decision 1), and prints a one-use code of 32 bytes from
`crypto/rand` once. The store keeps a hash only. The person signs in, with a
fresh authentication, and sends the code in the body of a `POST`. The server
then makes the `site-administrator` grant in one transaction with the use of
the code, a stored marker "first administrator made" and an audit entry
that names the host command as the creator, the reason and the sign-in ID
of the person.

- Gives: proof of shell access to the host, in addition to the fresh
  authentication of the person. The grant has a person with a fresh step at
  the moment of the change.
- Costs: one more command that opens the database, which is a second
  exception to ADR 39 decisions 1 and 4. It needs shell access, which is
  harder in a container (`kubectl exec`). It needs a choice for recovery
  mode `none`.
- Risks: a person with shell access already controls the key-encryption key
  and the database (ADR 245 decision 1), so the command adds little
  exposure. A code in a terminal scrollback is a secret: it ends after 10
  minutes (the limit of ADR 237 decision 12) and after one use.

### B. A first-administrator code from the bootstrap admin token, through the API

The same code as option A, but the bootstrap admin token asks for it with
`POST /api/server/first-administrator {"principal": "<id>", "reason": "..."}`.
The route works only while the marker "first administrator made" is not
stored. After the marker, it answers `409` with its own error code. The use
of the code is the same as in option A: a fresh authentication of the
person, one transaction, the marker, the audit entry.

- Gives: no new command that opens the database (ADR 39 stays as it is).
  Works in a container and through the Helm chart with no shell. Works with
  local users and with persons of an OpenID or OAuth provider, because the
  code binds to a principal ID, not to a sign-in method. Fits ADR 6 ("for
  first boot") and ADR 252 (the bootstrap administrator is the granter).
- Costs: one public use route and one route for the bootstrap admin token,
  each in the route test, the policy check and the OpenAPI document.
- Risks: the person who holds the bootstrap admin token before the first
  administrator exists can make an administrator. That person can already
  do each action that needs no fresh step, so the new exposure is small.
  The marker closes the route for good: a later loss of every administrator
  goes through recovery (ADR 57, ADR 245), not through this route.

### C. The temporary administrator signs in to the console

The host command prints a one-time sign-in link, as `dev` does (ADR 237
decision 12), and the link starts a console session of the temporary
administrator. The sign-in is fresh. The temporary administrator then makes
the grants in the console.

- Gives: reuses the temporary administrator, its expiry and its audit.
- Costs: changes ADR 245 decision 4 and ADR 297 decision 20. Each first
  setup is then a break-glass use, with a reason and a review (ADR 245
  decision 3), and it trains operators to use break-glass for routine work.
- Risks: the command refuses in recovery mode `none`, so an installation in
  that mode still has no path. The expiry ends the access of the temporary
  administrator but not the grants that it made for others (ADR 245
  decision 13), which is correct here but needs an operator check.

### D. The bootstrap admin token may make only the first grant

The bootstrap admin token may make one `site-administrator` grant on the
installation, to a person, while the marker "first administrator made" is
not stored. A code rule in the catalog names this exception.

- Gives: the smallest change. The current guide sentence becomes true for
  one grant.
- Costs: one code rule.
- Risks: the grant has no fresh step of any person: no human proves
  presence at the moment of the change. It is a direct exception to ADR
  227 decision 6. A check of "no site administrator exists" in place of a
  stored marker would open the door again when the last administrator
  goes, so the marker is required.

### E. Do nothing, fix the guides

The guides say that only `dev` gives a first administrator session.

- Gives: no code.
- Costs: password sign-in and OpenID sign-in cannot be used on a production
  installation. This is not acceptable.

## Precedent

Pages fetched on 2026-11-02 unless marked.

- **Keycloak** (fetched): `KC_BOOTSTRAP_ADMIN_USERNAME` and
  `KC_BOOTSTRAP_ADMIN_PASSWORD` make an account "only during the initial
  start of the Keycloak server when the master realm doesn't exist yet".
  The account "should exist only for the duration necessary to perform
  operations needed to gain permanent and more secure admin access", and
  the console shows a banner. The pages give no expiry time. This is close
  to options B and D: a first-boot credential that works only before the
  first setup.
- **HashiCorp Vault** (fetched): "The initial root token generated at
  `vault operator init` time -- this token has no expiration." "Root
  tokens can do anything in Vault." They "are revoked immediately after
  they are no longer needed." A new one needs "the permission of a quorum
  of unseal key holders". This is the shape of the bootstrap admin token
  today.
- **Grafana** (fetched in part): the first sign-in uses `admin` for user name
  and password, and "you will see a prompt to change the password". The
  settings page was not read to the `[security]` section, so this RFC does
  not quote `disable_initial_admin_creation`.
- **Kubernetes bootstrap tokens** (from a summary of the fetched page, not
  a quote): a token has an absolute `expiration`, authenticates as
  `system:bootstrap:<token id>` in the group `system:bootstrappers`, and
  RBAC limits what it can do. A controller deletes expired tokens. This is
  the shape of options A and B: a short-lived credential with one narrow
  use.
- **GitLab** (not fetched: the two pages read do not mention it): the Linux
  package writes an initial root password to
  `/etc/gitlab/initial_root_password` and deletes the file after 24 hours.
  A reviewer must check this before an ADR quotes it. This is the shape of
  option A: a host file proves host access.
- **Jenkins and Argo CD** (not fetched, from ADR 237 decision 12): they
  write the first admin secret to a file, not to the log.

## Proposal

Option B. The bootstrap admin token asks for a one-use first-administrator
code for one principal. The person uses the code in a console session with
a fresh authentication. One transaction makes the grant, spends the code
and stores the marker. After the marker, the route is closed for good.

Reasons:

- The grant happens in a session of a person with a fresh authentication.
  So ADR 227 decision 6 stays true for the change itself: the token makes
  an offer, and the person's fresh step makes the grant. The audit entry
  names both: the bootstrap administrator as the creator, and the person
  and the sign-in ID as the one who took the role.
- It keeps ADR 39: no new command opens the database.
- It works for every sign-in method and in every recovery mode.
- The marker is a stored installation fact with an API read, so "why could
  the bootstrap token do this" has an answer: the marker was not set, and
  the code rule of the catalog names the exception (no hidden rule).

The defense-in-depth layers that the ADR must name, each with its own test:

1. Boundary: the route accepts only a principal ID of a live, enabled
   person of the installation, and a reason with no control or format
   characters. It refuses a break-glass person, a temporary administrator,
   a service and a `dev:` subject.
2. Containment: the code is 32 bytes from `crypto/rand`, sent in a `POST`
   body only, never logged, stored as a hash, compared in constant time.
3. Limit: one code at a time, 10 minutes, one use, one role
   (`site-administrator`) on the installation only.
4. Authorization at the point of use: the use checks in the store
   transaction that the marker is not set, that the code names the
   principal of the session, and that the session's authentication is
   fresh.
5. Fail closed: an unknown state, a set marker or a failed audit write
   refuses, and the refusal writes an audit entry.

The guides change with the code: `docs/guides/console-sign-in.md` (the
sentence on the first roles), `docs/guides/local-users.md` (a section "The
first administrator") and `docs/guides/workload-identity.md` (step 1 needs a
console session, issue #2196).

## Open questions

1. Which option: A, B, C or D? The recommendation is B.
2. Must the person of the first-administrator code have a second factor, or
   is the fresh authentication of ADR 285 decision 21 enough? The
   recommendation is: the same fresh authentication as every other change.
3. Must the first setup also ask for the two break-glass accounts of ADR 70
   decision 24 before it sets the marker, or is that a later step?
4. Must the use of the code end the bootstrap admin token, or only show the
   prompt of ADR 53 decision 9? The bootstrap admin token comes from the
   environment (ADR 6), so the server cannot delete it. It could refuse it
   after the marker, with a setting to allow it for a restore (ADR 39).
5. Does an installation that has a person with `site-administrator` before
   this change (an upgrade) get the marker at the first start? The
   recommendation is yes, set by the migration from the stored grants, with
   an audit entry.

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write
  waits for another installation (ADR 248 decisions 3, 6 and 9). The
  marker and the grant are installation data, not namespace data, and
  each installation makes its own first administrator.

## Outcome

Empty until the RFC is accepted or dropped.
