## Context

mTLS exchange reuses the trust rule model and the stored trust token of ADR 284. The server ends TLS, checks the client chain against a per-namespace trust bundle, matches one trust rule on the certificate identity, and issues a short-lived stored token. The CLIs then use that token as a bearer token. The trust rule model is the one model of ADR 54 shared with JWT exchange; this change adds only the mTLS match and bundle, not a second rule model.

## Decisions

1. **Trust bundle and exact certificate match.** Add a trust bundle object per namespace and a certificate match on trust rules: bundle reference plus SPIFFE ID exact match in URI SAN, with optional exact match on one more named certificate field per ADR 54 decision 4. Exact match only. No substring, no regular expression. Reuse the one trust rule model of ADR 54 shared with JWT exchange.
2. **One mTLS exchange endpoint outside the tool-neutral API.** Add one mTLS exchange endpoint at POST /trust/v1/namespaces/{namespace}/exchange-mtls that takes the client certificate from the TLS handshake. Direct TLS only in this change: a request with no client certificate, including any request where an operator TLS proxy ended TLS, gets no token and no proxy header is read. The exchange client is any HTTPS client such as curl. Neither the Pulumi CLI nor OpenTofu sends the certificate. A proxy header path is deferred to a later change.
3. **Save-time checks, atomic exchange, rotation and revocation.** Check bundle parse at save, chain checks at exchange, and rule match plus issue in one store transaction snapshot. Bundle rotation is overlap of two stored bundles. Revocation is a stored serial deny list on the bundle with fail-closed reads.

## Structure

Unexported type mtlsExchange with constructor newMtlsExchange that takes only a verified chain, a parsed SPIFFE ID and a namespace fence. Only the constructor can make a value, and it checks bundle, expiry and exact match invariants.

## Risks

- Tool CLIs never send client certificates in this change, so the exchange client is separate from the CLIs.
- Operators who end TLS at a proxy use JWT exchange or service tokens until a later proxy attestation change ships.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| TLS handshake and chain check | TLS layer checks chain to configured bundle, expiry, purpose, size limits and stored revocation state before any rule logic. | Expired chain, wrong CA chain, wrong purpose chain, oversized chain, revoked serial and proxied request with no client certificate each get no token. |
| Trust rule match | Service layer matches one stored rule in the request namespace with exact SPIFFE ID match and bundle match, and refuses cross-namespace rules and ambiguous identities. | Valid certificate with unknown SPIFFE ID, valid ID from another namespace, duplicate rules and several or zero SANs each get no token. |
| Issue and use of trust token | Handler checks authorization at point of use for the issued token and writes the exchange audit entry with namespace, rule, principal and certificate fingerprint. | Token from namespace A gets 403 on a unit of namespace B and the audit export holds the exchange entry. |
