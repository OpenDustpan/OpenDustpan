## Context

Namespace trust bundle store plus trust rule CRUD under the namespace path, before any mTLS exchange.

## Decisions

1. **Versioned bundle, no silent trust change.** Bundle is one versioned object per namespace with append-only CA versions, active version pointer, PEM parse at save, expiry check at save and at use, rotation adds a version then moves the pointer, no delete of the active version.
2. **Rule references stable IDs and refuses own issuers.** Rule stores namespace ID, stable rule ID, stable principal subject, bundle version reference, SPIFFE ID pattern or certificate field conditions, no issuer URLs of this installation per ADR 284 decision 3, checked at save.

## Structure

Unexported bundleStore and ruleStore types; only constructors make values and constructors check PEM set, expiry, namespace fence, and stable IDs.

## Risks

- Bundle rotation overlap permits two CAs at once by design; docs state the overlap window.
- Proxy TLS termination question stays with the later exchange part.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port checks namespace fence, PEM shape, expiry, SPIFFE ID shape, and refusal of own issuers before write. | Save expired CA, malformed PEM, and own-issuer URL; each save is refused. |
| service | Service checks namespace:manage-trust action on the namespace object and fresh authentication before bundle or rule write. | Call write without grant or with stale authentication; server refuses with 403. |
| HTTP handler | Handler declares action and object, maps tool errors to stable codes, writes audit entry with actor and reason. | Send cross-namespace rule create and replayed write; handler refuses and audits the refusal. |
