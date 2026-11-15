## Context

Merge commit logic, parent assignment, serial assignment, fence check, and sealed detection live in concurrent-applies-merge-core. This part records the committed result so history stays complete, audit stays traceable, and sealed bytes stay sealed.

## Decisions

1. **Record parent and claim on the version.** Store parent version ID and claim ID on each new state version row in SQL in the baseline in name order. History reads these stored fields and shows them. Audit writes one merged entry per merge commit with claim IDs and parent IDs in the same fenced transaction. Tool-compatible documents keep their shapes. Claim display lives in the tool-neutral history view.
2. **Server-built record with opaque sealed bytes.** Build the merge record in the server from committed rows only. Never accept a client-supplied merge record. Treat sealed state bytes as opaque. Audit holds identifiers and masked values only, never plain secrets.

## Structure

Unexported mergeRecord built only by newMergeRecord, which requires parent IDs, claim IDs, and sealed values before it returns a value.

## Risks

- History shows two versions with no claim link and users cannot trace which claim made which version.
- Audit misses one parent and review cannot prove both applies merged.
- Secret plain text leaks into the merged audit entry or into history output.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port adapter rejects a merge record with missing parent IDs, missing claim IDs, or non-opaque secret bytes before the service call. | Send a merge record with a plain secret directly to the adapter and prove the adapter refuses it even if the service were bypassed. |
| service | Service builds mergeRecord only through newMergeRecord, commits version rows with the merged audit entry in one fenced transaction, and fails closed on unsealed output. | Fail the audit write after the version write and prove the store holds no new version. Restart before commit and prove no partial record. |
| HTTP handler | HTTP handler checks unit read action for history and audit read action for the merged entry, on the namespace object, and returns 403 on denial. | Request history and the merged entry without the action at each handler and prove 403 even if the service would allow it. |
