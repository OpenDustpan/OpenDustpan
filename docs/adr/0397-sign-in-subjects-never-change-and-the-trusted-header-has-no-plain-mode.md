# 397. Sign-in subjects never change, and the trusted header has no plain mode

Status: accepted · 2026-11-24. Records the decision of the project owner of 13 November 2026, in the owner's words

Changes:

- Changes ADR 321 decision 29: a CAS provider needs a subject attribute. The subject is never the CAS `user`.
- Changes ADR 321 decision 36: the trusted header has the `jwt` mode only. The plain mode is removed.
- Extends ADR 321 decision 7: an OAuth 2.0 provider takes the subject from the `sub` claim only. The subject claim is not a setting.

## Context

ADR 321 decisions 29 and 36 are marked (PM). They are not owner decisions. They let a CAS provider use the CAS `user` as the subject, and they give the trusted header a plain mode with a shared secret. The `user` of a CAS server and a header value can change for the same person. A changed subject makes a new principal, or lets a person take over the principal of another person.

On 13 November 2026 the project owner approved queue item P20, item 18 (issue #2915), "as written":

"(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only)."

The reason, in the owner's words: "Entra, Okta and Kubernetes guidance". Also: "no users, so it costs nothing now and can be loosened later".

The OAuth 2.0 provider of main has a setting for the name of the subject claim. The GitHub example of the console guide uses it with the value `id`.

## Decision

1. **CAS needs a subject attribute (owner, 2026-11-13).** A CAS provider has a subject attribute that the administrator names. A provider without it is refused when the administrator saves it. The CAS `user` is never the subject.
2. **The trusted header has no plain mode (owner, 2026-11-13).** The only mode is `jwt`. It keeps the two layers of ADR 321 decision 36: the peer list and the signature check. The code for the plain mode and its shared secret is removed. A provider that has the plain mode is refused.
3. **An OAuth 2.0 provider uses `sub` only (owner, 2026-11-13).** The subject is the `sub` claim. The provider has no setting for another claim. A response without `sub` gets the refusal reason `sign-in.subject-missing` in the audit log. The browser gets the generic refusal.
4. **LDAP keeps the immutable identifiers (owner, 2026-11-13).** The subject of an LDAP provider is `entryUUID` or `objectGUID`.
5. **An unlisted peer gets a refusal with a record.** A request from a peer that is not in the proxy list gets the reason `sign-in.untrusted-proxy`. The audit entry holds the TCP peer, never a forwarded header.
6. **The rule can be loosened later (owner, 2026-11-13).** A later ADR may allow a mutable source. It needs a decision of the owner.

## Consequences

- ADR 321 lists this ADR in its "Changed by" list. The words of decisions 29 and 36 stay as written.
- The change `sign-in-methods` (version 6) implements this ADR. It removes the subject claim setting from the API, the settings, the baselines and the backup columns.
- A provider that uses another claim, for example GitHub with `id`, does not work as an OAuth 2.0 provider. The console guide has no example for it.
- The CAS guide says that the subject is never the CAS `user`. The trusted header guide says that the plain mode does not exist.
