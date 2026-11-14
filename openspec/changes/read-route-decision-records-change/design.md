## Context

Refusals at state.write already name a decision record ID, but no route reads the record. This change adds a read-only route for stored records and fixes the approval refusal so it names a command that exists.

## Decisions

1. **Read one record by ID with named read guards.** New read route GET one record by its stable gdr- ID within its namespace scope. The guard for a namespace record is the namespace gate read action, and for a record of an access limit on an installation object it is installation:read-gates. The handler checks the guard before any read. An in-scope caller without the read action gives 403. A caller with no namespace access, an unauthenticated caller, or a missing, expired, malformed, or foreign ID gives 404 with no content.
2. **Errors carry the record ID and secrets stay masked.** The service returns typed sentinels for not found and not allowed, and the HTTP adapter maps them to gate.not-found with status 404 and gate.denied with status 403, with the record ID in each in-scope error body. Secret values in fact documents are masked on read unless the caller holds the secrets read action of the scope. A read that races expiry or restore gives 404.

## Structure

Unexported decisionRecord with a constructor that checks the gdr- ID, the namespace scope, and the required fact hash before the value can be built.

## Risks

- A read route that leaks records across namespaces breaks tenant isolation.
- A refusal that names a command that does not exist breaks copy and blocks scripting.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler checks the namespace gate read action for namespace records and installation:read-gates for installation limit records before any read. | Read an own-namespace record without the read action, a foreign-namespace record with the read action, and the same record with no authentication; expect 403 only for the first and 404 with no content for the others. |
| service | Service loads by stable gdr- ID, rejects malformed IDs, enforces the scope check again, and masks secret values in fact documents. | Call the service with a forged scope, a non-gdr- ID, IDs with path separators and SQL metacharacters, and facts with secret values; expect a not-found or denied sentinel with masked output. |
| port adapter | Store query filters by namespace fence and record ID with placeholders only in the same transaction shape as the write. | Pass an ID with path separators and SQL metacharacters; expect no row and no error leak. |
