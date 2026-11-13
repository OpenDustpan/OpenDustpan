## Context

The sign-in service already runs one order for LDAP, OIDC, CAS and passkeys. This change adds SAML, plain OAuth 2.0 and signed-token trusted header to the same order, with no new token type.

## Decisions

1. **Adapters behind existing ports.** Reuse the ports of the one sign-in path. Owner decision, 13 Nov 2026 (queue P20): the subject is the OAuth `sub`, a named CAS attribute, or the signed claim of the header token.
2. **SP-started first.** SP-started SAML is the default. IdP-started needs a setting, a one-time assertion ID, a maximum age, audience and recipient checks, and is never fresh.
3. **One-time IDs in the database.** Request IDs and assertion IDs live in a table with a unique key and an expiry. If the store fails, the sign-in is refused.
4. **Switches are settings.** The trusted-header switch and the IdP-started switch are keys of the generic settings API, not new routes.
5. **Separate proxy list.** The provider setting `header.trustedProxies` is read at each request and is separate from the trusted-proxies flag.

## Structure

The sign-in service holds an unexported `methodSet` that only `newMethodSet` can build. It refuses a duplicate provider ID, a header provider with an empty proxy list or no signed-token mode, an OAuth provider whose subject claim is not `sub`, a CAS provider with no subject attribute, and IdP-started SAML with no one-time ID store.

## Risks

- IdP-started SAML widens replay risk; it stays off unless the setting is on.
- A wrong proxy list grants access; the list and default-off guard it.
- The owner decision is provisional; a later change can loosen the subject rules.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Boundary check | Adapters check SAML audience, recipient, time and signature, the OAuth `sub`, the CAS attribute, and the header peer address. | TestAdapterHostileInputs: wrong audience, expired assertion, missing sub, plain CAS name and unnamed peer each give no sign-in. |
| Service use check | The service checks provider state, subject, assertion ID reuse and disabled person in one order, and fails closed when the ID store fails. | TestServiceHostile: replayed ID, concurrent use, store outage and disabled person each refuse. |
| HTTP containment | The handler copies declared fields only, strips CR and LF, and writes one audit entry for each attempt. | TestHandlerHostile: extra fields and CRLF in header values give no session. |
