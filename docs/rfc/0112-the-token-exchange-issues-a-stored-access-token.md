---
rfc: 112
title: The token exchange issues a stored access token
status: accepted
created: 2026-10-30
adr: 284
---

# RFC 112. The token exchange issues a stored access token

Status: accepted ([ADR 284](../adr/0284-the-token-exchange-issues-a-stored-access-token.md)).
This RFC proposes an amendment of
[ADR 54](../adr/0054-workload-identity-oidc-jwt-and-mtls.md) on two points.
The code of the token exchange (pull request 2105) follows the proposal. The
project owner accepted the proposal on 30 October 2026.

## Problem

ADR 54 has two sentences that the implementation of the token exchange does
not follow word for word.

1. Consequences: "The exchange endpoint and the mTLS check are
   `Authenticator` adapters behind the port of ADR 6. Each runs the
   conformance suite of the port."

   The exchange endpoint is not an `identity.Authenticator`. It checks the
   JWT once and issues a stored access token of the kind `trust`. On each
   later request, the existing access-token authenticator
   (`accesstoken.Service`) checks that token and gives the credential
   `trust-token`. That authenticator runs the conformance suite of the port
   with an exchanged token.

2. Decision 3: "The server treats no issuer as a special case."

   The trust rules refuse each issuer at or under the public URL of this
   installation, at the create of a rule and again at each exchange.
   [ADR 204](../adr/0204-opendustpan-as-an-oidc-issuer.md) decision 7 already
   decides this refusal ("The trust rules of ADR 54 also refuse each issuer
   of this installation"), and the status line of ADR 54 says that ADR 204
   changes decision 3 in part. The text of decision 3 still reads as if no
   such issuer exists.

CLAUDE.md asks for an RFC first when a change contradicts an ADR. A security
review of pull request 2105 found both points and recommends that the design
stays.

## Options

### Point 1: where the JWT is checked

A. **Exchange, then a stored token (STS style).** The endpoint checks the JWT
   once, against the rules of the namespace, and issues an access token of
   ADR 6 that the store keeps. Later requests send that token, and the
   access-token authenticator checks it.

   - Gives: decision 2 as written ("issues a short-lived OpenDustpan access
     token (ADR 6)"). The token has an ID, a lifetime from the token policy,
     an audit entry for its issue, and a revoke through the routes of the
     service tokens. One JWT check for each exchange, not for each request.
     The tool CLIs send a bearer token today, so they work with no change.
   - Costs: the endpoint itself is not an `Authenticator`, so the sentence
     of Consequences is not true as written.

B. **An `Authenticator` that accepts the JWT on each request.** Each request
   carries the JWT of the workload, and an adapter of the port checks it.

   - Gives: the sentence of Consequences as written.
   - Costs: no issued token, which decision 2 asks for. A JWT check and,
     for a discovery issuer, a key read on each request. No token ID to
     revoke and no token entry in the audit log. The JWT of the CI platform
     goes to each route of the server, so a leak of one request log leaks a
     credential that is valid at every route. The lifetime is the lifetime
     that the issuer set, not one that the token policy bounds.

C. **Both.** Build B beside A.

   - Costs: two paths that must stay equal in their checks, and the costs
     of B.

### Point 2: the issuers of this installation

A. **Refuse them**, as ADR 204 decision 7 decides, and write the refusal into
   the text of decision 3.

   - Gives: a token that this installation signs (a profile token of ADR 204)
     never becomes an access token of this installation. A profile token goes
     to third parties, for example to a cloud provider in a login. Without
     the refusal, a rule that names this installation as its issuer would let
     each holder of such a token send it to the exchange and get an access
     token of a principal of the namespace.
   - Costs: decision 3 gets an exception.

B. **Treat every issuer the same**, as decision 3 reads alone.

   - Costs: the attack above. It also contradicts ADR 204 decision 7.

## Proposal

Point 1: option A. Point 2: option A.

Amend ADR 54 as follows:

- In Consequences, replace the first item with: "The exchange endpoint checks
  the JWT once and issues a stored access token (ADR 6) of its own kind. The
  access-token authenticator checks that token on each later request, and it
  runs the conformance suite of the port with an exchanged token. The mTLS
  check is an `Authenticator` adapter behind the port of ADR 6, and it runs
  the conformance suite of the port."
- In decision 3, add: "The trust rules refuse each issuer of this
  installation (ADR 204 decision 7)."

The security reason for point 1 is the same as for the access tokens of
ADR 53: a credential that the server issues has a bounded lifetime, an ID, an
audit entry and a revoke. A JWT that each request sends has none of these on
the side of the server.

## Open questions

- mTLS (decision 4) is not built yet. This RFC keeps the mTLS check as an
  `Authenticator`. Should an mTLS exchange that issues a stored token, as
  RFC 20 asks for tools that cannot send a client certificate, follow
  option A of point 1 too?

## Checklist

- [x] Tolerates asynchronous replication between installations: an exchange
  writes its token and its audit entry in the namespace of the rule, under
  the fence of that namespace, in one transaction, and waits for no other
  installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Accepted on 30 October 2026 by the project owner: the token exchange issues a
stored, short-lived access token. [ADR 284](../adr/0284-the-token-exchange-issues-a-stored-access-token.md)
holds the decision and amends ADR 54 on both points (option A of point 1 and
option A of point 2). The open question on mTLS stays open.
