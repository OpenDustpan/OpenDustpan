## Context

Restore must not replace access tables by default. An explicit installation setting widens server:restore only when a site administrator turned it on, with each change on record and the two-path guard intact.

## Decisions

1. **API-only setting.** The setting is a stored installation boolean, default false, registered as a key in the generic settings API with no flag, env, or file layer and a locked API-only write with a fresh step. The settings read shows the effective value. No separate route is added.
2. **Snapshot read at apply time.** The restore apply step reads the stored value once at the start of the apply transaction and branches on that snapshot in the same transaction. A read failure or unknown stored text fails closed and refuses the access-table part. The widened path still runs the two-working-path check in the same write transaction, which serializes it with a concurrent break-glass disable.

## Structure

An unexported installRestoreAccess value built only by a constructor that maps empty to false and accepts only true or false; any other stored text returns an error and the caller fails closed.

## Risks

- An operator turns the setting on and forgets it, which leaves a wide restore path open.
- A caller without server:restore-access restores access tables while the setting is on without a clear audit trail.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Generic settings handler checks the gated actions on the installation and a fresh step in the window for this locked key, and rejects without them. | Send a setting change with only server:restore and with a stale fresh step; each gets 403 and the value stays false. |
| service | Service constructor builds the setting only from true or false, default false; unknown stored text is refused and fails closed. | Write an invalid stored value and read it; the service refuses the read instead of treating it as on. |
| port adapter | Restore store adapter checks the snapshot value plus the caller action in the same transaction: off needs server:restore-access, on accepts server:restore, and both refuse when the result leaves fewer than two working break-glass paths. | Restore a backup that drops the last break-glass path with the setting on; the apply refuses with 409 and changes nothing. |
