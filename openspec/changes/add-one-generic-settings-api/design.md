## Context

One settings registry serves the installation scope and each namespace scope. Each declared key holds type, default, validation, sensitive mark, file-only mark, weaker direction, audit name and authorization action. The effective value resolves flag, env, file, database, default in that order. The temporary administrator limit is one declared installation key.

## Decisions

1. **One settings resource per scope.** GET returns each declared key with effective value, source layer, default, locked flag and the one revision, on both scopes. PATCH takes JSON Merge Patch, may carry a base revision, and refuses unknown keys (422), locked keys (409), file-only sensitive keys (409) and invalid values (422). It applies all database values in one transaction or none. A null deletes the database value.
2. **Fresh step for weaker changes.** The service finds the weaker direction of each key before any write. If one key is weaker, the whole PATCH needs a fresh step, also when other keys are stronger. A longer tempadmin.session-limit is weaker. A shorter one is not. A failed check answers 403 mfa.fresh-step-required and changes nothing.
3. **Revision, reload and audit.** A PATCH with a base revision that is not current is refused with 412. A reload that changes an effective value or a source moves the revision and writes an audit entry with the old and new source and value. The audit entries of a PATCH are written in the same transaction as the change, so an audit failure fails the PATCH. A PATCH in flight during a reload takes the transaction order.
4. **Idempotency.** A PATCH with no Idempotency-Key is refused. A replay with the same key and body answers the stored answer and writes no second audit entry. A replay while the first request runs waits for it. The same key with another body is refused.
5. **Temporary administrator session limit.** Owner decision, 13 Nov 2026 (queue P12, provisional). The key tempadmin.session-limit holds a duration from 1 minute to 24 hours, default 1 hour. A new record and its token end at create time plus the shorter of the limit and the token policy maximum. The creator reads the limit in its own transaction. A stored value outside the range cannot be written. If one is read anyway, the creator fails closed, writes an audit entry and logs the key. It never falls back to 24 hours. The host command has a reset option that deletes the stored value with an audit entry, so recovery needs no API and no other administrator. The audit entry of a record names the limit used. An existing record keeps its stored end time.

## Structure

The unexported settingsRegistry has only NewSettingsRegistry as constructor, so no caller can invent a key. The unexported tempAdminLimit has only ParseTempAdminLimit as constructor, which checks the range. The creator takes a tempAdminLimit value, not a raw string.

## Risks

- A wrong weaker-direction mark skips the fresh step or asks too often.
- A lock misread lets a database write override a static lock.
- A bad stored duration blocks the creation of a temporary administrator; the reset option and the audit entry name the key.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | The handler checks the action for the scope, requires the Idempotency-Key, validates key names and values, refuses locked and file-only keys, and enforces the fresh step before the store write. It stays even though the service checks too. | PATCH without the manage action, without a key header, with an unknown key, with a locked key, and with a weaker value and no fresh step: each is refused and the store shows no change. |
| service | The service resolves the layer order, applies the all-or-none transaction, checks the base revision, bumps the revision and writes one audit entry for each changed key with old and new values. | A two-key PATCH with an invalid second key, and a PATCH whose audit write fails, leave the keys and the revision unchanged. |
| port adapter | The store keeps declared keys only and keeps database values apart from static values. The creator reads the limit only through ParseTempAdminLimit. | Insert an undeclared key row and an out-of-range limit row directly. The read hides the first. The creator refuses the second. |
