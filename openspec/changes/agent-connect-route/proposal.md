---
version: 2
---

## Why

ADR 129 (accepted 2026-10-18, owner) decision 29 says: "`PUT /api/agent/connection` checks the document, stores it as a pending document with the ID of a new probe as its key, starts that probe, and answers 202 at once with the probe ID." Main has no route that connects a model. An administrator can write the document, the secret items and the probe, but nothing makes the document the connection. The route is also the first step of Dusty: the connect transaction makes the principal of Dusty (the change `dusty-agent-principal-on-model-connect`).

## What Changes

- `PUT /api/agent/connection`: reads the document, checks it, checks `If-Match`, stores a pending document, starts a probe, and answers 202 with the probe ID (decision 29).
- The cancel rule: the `PUT` cancels, in its own transaction, each earlier probe that has a pending document, and removes those documents.
- The last step of the probe (the promote step): calls `agent.ConnectModel` of the change `dusty-agent-principal-on-model-connect`, in the transaction of its own write, only when the `If-Match` version is still the version of the connection.
- The check of the version: a missing `If-Match` answers 428. A version that is not the current one answers 412 at the start of the `PUT`, and the promote step checks it again.
- The check of the value of secret items: the route calls `agent.CheckNoSecretValue`.
- The audit entry of a connect or a change (decision 33): `changes` holds the old and the new document with references only, and the probe result.
- The change of `agent.ConnectModel` of `dusty-agent-principal-on-model-connect`: its connection write is `ConnectionStore.Promote` of the pending document, not `SettingsStore.PutConnection`.

## Out of scope

- The probe, its checks and `POST /api/agent/connection/probe`: the change `agent-connection-probe`.
- The document, the secret items and the read and delete routes: the change `agent-connection-document-and-secrets`.
- The adapters: `agent-model-adapters`. The filter: `agent-secret-filter`.
- The principal, the sponsors and the ceiling grants of Dusty: `dusty-agent-principal-on-model-connect`.
- The console form.

## Depends on

- `agent-connection-document-and-secrets` (`ConnectionSpec`, `ConnectionStore`).
- `agent-secret-filter` (`CheckNoSecretValue`).
- `agent-model-adapters`, `agent-connection-probe` (`Probes.Start`).
- `dusty-agent-principal-on-model-connect` (`agent.ConnectModel`).
- Main: `ActionServerManageAgent`, the audit log.

## Usability

The persona is the administrator who connects a model. The administrator sends the document with `PUT` and gets a probe ID at once. The administrator reads the probe and sees the result of each check. A stale version answers with the one command that helps: "Read the connection with GET /api/agent/connection and send the PUT again." The docs page has the full path from the document to a connected model with the commands, and a test runs each one.

## What main already does

- No route under `/api/agent/connection`. `SettingsStore.PutConnection` in `internal/agent/settings.go` has no caller.

## ADR check

- ADR 129 decisions 29 and 32 (owner, 2026-10-18): this change builds the `PUT` route and the last step of the probe. It changes none of their words.
- ADR 354 and RFC 121 decision 7: the connect transaction makes Dusty. This change gives the transaction its caller.
- ADR 17: the route declares its action and object. ADR 250 and ADR 364: the layers table in design.md.

## Open questions

none
