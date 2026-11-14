## Context

Gate configuration is security code. Writes need action checks, scope checks, fresh authentication, limits, version checks and audit entries. The service exists; this change serves it at every scope.

## Decisions

1. **Routes on ConfigService.** Serve gates, approval rules, approval defaults and the carry-over setting at four scopes. The unit scope is the by-name path of ADR 355. Each route declares the read or manage action of its scope.
2. **Stable unit key.** Key gate rows of a unit on the stable unit ID. A rename or a restore keeps the rows. A new unit that reuses a name starts clean.
3. **Version check in the transaction.** The adapter compares If-Match with the stored version in the write transaction. A write without If-Match to an existing object fails.
4. **Fresh authentication.** A change that needs fresh authentication fails closed with mfa.fresh-step-required for an access token and for an old session.

## Structure

An unexported scopeKey type, made only by a constructor, holds the scope kind, the namespace and the stable unit ID. The constructor rejects a scope that does not exist, so a write cannot cross scopes.

## Risks

- A reused name reattaches the policy of a deleted unit.
- Two managers write one row with the same version.
- A weaker final gate changes with no reason in the audit entry.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | The handler checks the action of the addressed scope and answers 404 for an object of another scope. | A token of another namespace reads a unit gate and gets 404. A write with no manage action gets 403. |
| service | The service checks limits, If-Match, fresh authentication and the scope key before a write. | Save over the limit, with a stale version, without If-Match and without fresh authentication. Each save fails closed. |
| port adapter | The adapter uses placeholders, compares the version, checks the fence and writes the audit entry in the same transaction. | Inject SQL text in an ID. Run two writes with one version. Fail the audit write and check that the row is not stored. |
