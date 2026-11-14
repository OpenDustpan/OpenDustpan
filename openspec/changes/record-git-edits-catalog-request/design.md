## Context

A self-service request has one stored document in the server and one request file in git at the ADR 111 path. A person with write access edits the file directly and pushes, bypassing the API. The server learns of the push through a forge webhook. Without a record, the next read or write-back loses the git edit or overwrites it without trace. This change is not spec-ready: build waits for the short drift-record ADR.

## Decisions

1. **Compare canonical bytes on each push delivery.** Parse each request file named by the delivery, compute canonical bytes of the stored request and of the git document with catalog.Document.Canonical, write a drift record when they differ and clear it when they match. The webhook answers 202 on parse failures and records invalid-file. A repeated delivery id is idempotent; the last distinct delivery for one repo and ref wins.
2. **Store drift beside the stored request with an explicit exit.** Store drift keyed by the stable request ID beside the stored request with status drifted, in-sync, invalid-file or file-removed, plus commit id, changed top-level field names, time, and error text. The webhook leaves the stored document unchanged. Exit is a push of matching bytes or an authorized submit of a document that matches the git bytes. The drift read uses GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift with the stable ID, never the renamable name.
3. **Catch up at start without a poll.** At server start and on a manual redelivery decided from effect state, the server reads the current file at the ADR 111 path for each request and runs the same compare. No timer polls the forge.

## Structure

Unexported type gitDrift with constructor newGitDrift(stored, git catalog.Document, commit string) that checks canonical bytes, derives the changed field list, and refuses an empty commit id, so mismatched compare logic cannot build a drift value.

## Risks

- A push with many request files slows the webhook answer.
- A malformed request file in git confuses the reader without a visible status.
- A missed webhook during an outage leaves drift hidden until the next start or redelivery.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| webhook trust | Forge webhook signature and event secret check at the HTTP boundary; reject a bad signature with 401 and write no drift record. | Send a push delivery with a wrong signature and confirm 401 and no drift record. |
| path and parse | Strict path allowlist of catalog/<ns>/<bp>/<name>/request.json with each segment checked; strict ADR 111 parse; invalid files become invalid-file status. | Send a delivery naming catalog/../../etc/request.json and one with an unknown field; traversal writes nothing and the unknown field becomes invalid-file. |
| use check | Drift read needs self-service:read; write-back and apply check drift at use and refuse with 409 request.drifted. | Read drift without self-service:read and confirm deny; start write-back over drift and confirm 409 with no write. |
