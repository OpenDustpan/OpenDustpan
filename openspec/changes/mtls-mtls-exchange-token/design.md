## Context

The exchange is security code on an external TLS endpoint. It must fail closed on chain, time, revocation, SPIFFE ID, rule state, and principal state before it issues a stored trust token.

## Decisions

1. **Verified chain value only.** The handler takes the verified chain from the TLS layer and passes an opaque verified-chain value to the service; the service never parses PEM and never reads sockets.
2. **Single enabled rule match.** Rule match selects at most one enabled rule in the request namespace by bundle ID and exact case-sensitive SPIFFE ID; no match, more than one match, disabled rule, disabled bundle, disabled principal, or issuer of this installation refuses.
3. **Stored issue and shared revoke.** Match check and stored issue share one transaction with a version check on rule, bundle, and principal; a concurrent disable between check and commit aborts with no token. Issue writes the stored trust token, its expiry, and its audit entry in one transaction; revoke uses the existing service-token revoke routes.

## Structure

Unexported mtlsExchange with constructor that takes bundle store, rule store, token issuer, clock, and policy; only the constructor makes a ready value and it refuses nil stores.

## Risks

- A second rule with the same SPIFFE ID could make match ambiguous.
- A large chain or high exchange rate could load the verifier.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | TLS adapter checks chain to enabled bundle, expiry with no skew, name, and revocation from the bundle revocation source; refuses other CA, expired, not-yet-valid, revoked, unknown revocation status, missing chain, or missing URI SAN. | Send a chain from another CA, an expired leaf, a not-yet-valid leaf, a revoked leaf, an unreachable revocation source, no client certificate, and a leaf with no URI SAN; each gets a refusal and no token. |
| service | Service rechecks rule enabled, bundle enabled, exact case-sensitive SPIFFE match, single match, namespace match, principal enabled, and issue lifetime against policy inside one transaction. | Replay a valid chain against a disabled rule, a disabled bundle, a rule of another namespace, two rules with the same SPIFFE ID, a disabled principal, and an over-long lifetime; each gets a refusal. |
| HTTP handler | HTTP handler requires client-cert authentication on the exchange route, checks authorization action on the exchange object, and fails closed on any error with no partial write. | Call the route with no client certificate and with a forced store error; each gets a 401 or 500 with no token and a refusal audit entry. |
