# 54. Workload identity: OIDC and JWT token exchange, and mTLS

Status: accepted · 2026-10-13 · decision 3 changed in part by ADR 204. Decision 5 extended by ADR 223. Decision 3 and the first item of the consequences amended by ADR 284

Changed by:

- ADR 69 extends: the lifetime of an exchanged workload token.
- ADR 70 changes decisions 5 and 8: trust rules name only a principal its
  author can act as.
- ADR 204 changes decision 3: trust rules refuse each issuer of this
  installation.
- ADR 222 extends decision 5: an access limit holds for a trust rule token.
- ADR 223 extends decision 5: an access limit holds for a trust rule token.
- ADR 284 changes decision 3: the trust rules refuse the issuers of this
  installation.
- ADR 369 changes decision 6: a trust rule can opt in to the person who
  started the event, and four kinds of actor.

## Context

CI is the main client of a state backend. A static access token in a CI
secret lives long and can leak. Other products let CI exchange the OIDC token
of its own platform, for example GitHub Actions, for a short-lived token.
[RFC 20](../rfc/0020-access-tokens.md) lists this idea. ADR 6 says that
OpenDustpan always issues its own access tokens.

On 2026-10-12 the project owner wrote: "We should also support arbitrary OIDC
and JWT issuers and mTLS certificates (SPIFFE or otherwise) and discourage the
use of static tokens for machines and workloads." On 2026-10-13 the owner
chose "OIDC/JWT and mTLS in M4".

## Decision

1. **Workload identity is in milestone 4.** Two kinds are in scope: token
   exchange with OIDC and JWT issuers, and mTLS client certificates.
2. **Token exchange.** A workload sends a JWT from its issuer to an exchange
   endpoint. The server checks the signature, the issuer, the audience and
   the expiry of the JWT. Then it applies the trust rules. When a rule
   matches, the server issues a short-lived OpenDustpan access token (ADR 6)
   for the principal that the rule names.
3. **Any issuer.** The issuer can be any OIDC provider, found through its
   discovery document. It can also be a JWT issuer with no OIDC discovery,
   with signing keys that an administrator configures. The server treats no issuer
   as a special case.
4. **mTLS client certificates.** A workload can prove its identity with a
   TLS client certificate. The server checks the certificate against a trust
   bundle that an administrator configures. The identity can be a SPIFFE ID
   in the URI SAN, or another field of the certificate that a trust rule
   names.
5. **Trust rules per namespace.** A trust rule belongs to one namespace. It
   names the issuer or the trust bundle, conditions on the claims or on the
   certificate fields, and the principal that a match gets. The grants of
   that principal decide what it can do (ADR 52). A rule never gives access
   to another namespace.
6. **The principal is not a person.** A matched workload acts as a principal
   that is not a person, like the owner of a service token (ADR 53).
7. **The project discourages static tokens for machines.** Service tokens (ADR 53)
   stay available. The docs and the console recommend workload identity for
   machines and workloads first.
8. **Audit and API.** Each exchange and each trust
   rule change writes an audit entry. Each trust rule operation has a REST
   API and an action (ADR 17).

## Not decided here

[RFC 20](../rfc/0020-access-tokens.md) keeps these open:

- (e2e) Does the OIDC login flow of the Pulumi CLI work against a
  self-hosted backend?
- (e2e) Which tool CLIs can send a TLS client certificate. If a tool cannot,
  can a workload use mTLS at the exchange endpoint to get an access token
  for that tool?
- How mTLS works when a proxy of the operator ends TLS before the server.
- The lifetime of an exchanged access token, and its default.
- Whether an administrator can turn off static service tokens for a
  namespace.

## Consequences

- The exchange endpoint and the mTLS check are `Authenticator` adapters
  behind the port of ADR 6. Each runs the conformance suite of the port.
- The server gets OIDC discovery documents and signing keys from the
  network. It caches them, and the cache has a limit.
- A JWT library or an OIDC library is a new dependency. Ask the project
  owner before adding it.
- Each step of the token exchange gets an e2e step with a real issuer, or a
  test issuer, before it ships.
