# 284. The token exchange issues a stored access token

Status: accepted · 2026-10-30. Decided by the project owner on 30 October 2026. Accepts [RFC 112](../rfc/0112-the-token-exchange-issues-a-stored-access-token.md). Amends ADR 54 (decision 3 and the first item of its consequences)

## Context

[ADR 54](0054-workload-identity-oidc-jwt-and-mtls.md) decides the workload
identity of milestone 4. Its decision 2 says that the server checks the JWT
of a workload, applies the trust rules, and "issues a short-lived OpenDustpan
access token (ADR 6) for the principal that the rule names".

Two sentences of ADR 54 do not match the token exchange that pull request
2105 builds:

- The first item of its consequences says: "The exchange endpoint and the
  mTLS check are `Authenticator` adapters behind the port of ADR 6. Each runs
  the conformance suite of the port." The exchange endpoint is not an
  `identity.Authenticator`. It checks the JWT once and issues a stored access
  token. The access-token authenticator checks that token on each later
  request.
- Decision 3 says: "The server treats no issuer as a special case." The trust
  rules refuse each issuer of this installation. ADR 204 decision 7 decides
  this refusal, and the status line of ADR 54 says that ADR 204 changes
  decision 3 in part.

A security review of the pull request found both points.
[RFC 112](../rfc/0112-the-token-exchange-issues-a-stored-access-token.md)
gives the options. On 30 October 2026 the project owner accepted RFC 112.

## Decision

1. **The exchange issues a stored access token.** The exchange endpoint
   checks the JWT of a workload once, against the trust rules of the
   namespace. When one rule accepts it, the server issues a stored access
   token of ADR 6 of its own kind, `trust`, for the principal of the rule.
   The token has an ID, an audit entry for its issue, a lifetime from the
   token policy (ADR 69 decision 6) and a revoke through the routes of the
   service tokens.
2. **The access-token authenticator checks the exchanged token.** On each
   later request, the access-token authenticator checks the token and gives
   the credential `trust-token`. It runs the conformance suite of the port of
   ADR 6 with an exchanged token. The exchange endpoint is not an
   `Authenticator` adapter.
3. **The trust rules refuse the issuers of this installation.** A rule cannot
   name an issuer at or under a URL of an issuer of this installation (ADR 204
   decision 7). The server checks this when a person creates a rule and again
   at each exchange. Each other issuer is not a special case.

The first item of the consequences of ADR 54 now reads: "The exchange
endpoint checks the JWT once and issues a stored access token (ADR 6) of its
own kind. The access-token authenticator checks that token on each later
request, and it runs the conformance suite of the port with an exchanged
token. The mTLS check is an `Authenticator` adapter behind the port of ADR 6,
and it runs the conformance suite of the port."

Decision 3 of ADR 54 now also says: "The trust rules refuse each issuer of
this installation (ADR 204 decision 7)."

## Not decided here

- Whether an mTLS exchange that issues a stored token follows decision 1 too.
  RFC 20 asks for such an exchange for tools that cannot send a client
  certificate. The question stays open in RFC 112 and RFC 20.

## Consequences

- A JWT of a CI platform goes to the exchange endpoint only, not to each
  route of the server.
- An exchanged token is a stored access token. The token policy bounds its
  lifetime, the audit log records its issue, and an administrator can revoke
  it.
- A token that this installation signs, for example a profile token of ADR
  204, never becomes an access token of this installation.
- ADR 54 keeps its other decisions.
