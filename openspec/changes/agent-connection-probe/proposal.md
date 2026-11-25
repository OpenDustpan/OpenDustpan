---
version: 1
---

## Why

ADR 129 (accepted 2026-10-18, owner) says: "The capability probe is a durable operation." A connection document says what a server should accept. Only a call shows what it accepts. Dusty needs tool calls, and a small local model can answer with none. Without a probe, the operator learns this in the first run of a person. The probe runs at connect time, costs a known number of tokens, and shows the operator the field that needs a change.

## What Changes

- The durable operation kind `agent.probe` (ADR 72 decision 1) with its steps: eight checks in order, each with a timeout (decisions 29 and 30).
- The checks `reach`, `auth`, `tools`, `tool-result`, `transport`, `usage`, `reasoning`, `cache`, with the rules for a blocking failure and for a warning.
- The probe states `pending`, `running`, `passed`, `warning`, `failed`, `skipped`, `canceled` for the probe and for each check.
- `POST /api/agent/connection/probe`: starts a probe of a document with no store, answers 202 with the probe ID.
- `GET /api/agent/connection/probes/{id}`: the state of the probe and of each check, with the time, the tokens, the status class, the sentinel and a strict STE error.
- The probe ledger rows with the origin `probe` and no run, keyed by probe ID, check name and attempt number (decision 31).
- The start limit of 6 probes in each minute for the installation, answered with 429 and `retry-after`. The limit counts a `POST` of the probe route and a start from the connect route.
- The probe API for the connect route: `agent.Probes.Start(ctx, ProbeInput) (ProbeID, error)`, where the input is a pending document ID or a document with no store. The last step promotes a pending document only when the `If-Match` version is current, in one transaction (decision 29).
- The cancel rules of decision 29: a later start cancels each earlier probe that has a pending document; a disconnect cancels them in one transaction.
- The audit entry of a probe with no store (decision 33).

## Out of scope

- `PUT /api/agent/connection` and the 202 with a pending document: the change `agent-connect-route`. This change gives the route the `Probes.Start` function and the promote step.
- The adapters and the secret filter: the change `agent-model-adapters`.
- The document and the stored pending document: the change `agent-connection-document-and-secrets`.
- `agent.ConnectModel` and the principal of Dusty: the change `dusty-agent-principal-on-model-connect`.
- The console view of a probe.

## Depends on

- `agent-connection-document-and-secrets` (`ConnectionStore.PutPending`, `Promote`, `Delete`).
- `agent-model-adapters` (`agent.NewModel`, `SecretFilter`, the dial guard).
- Main: the durable operation runner (ADR 72), the token ledger (ADR 120 decision 20).

## Usability

The persona is the administrator who connects a model. The administrator reads the probe and sees one line for each check: the state, the time, the tokens. A failure names the field of the document to look at, and the one action that helps. Example: "The model gave no tool call in 2 attempts. Make sure that the model server has tool calls on." The probe never shows the error text of the server, because that text can hold a secret or an internal address. The docs page shows each check with a worked failure.

## What main already does

- The durable operation runner of ADR 72 in the store and service layers. No kind `agent.probe`.
- The token ledger of ADR 120 decision 20 for runs. No row with the origin `probe`.
- No route under `/api/agent/connection/probe`.

## ADR check

- ADR 129 decisions 29 to 31 and 33 (owner, 2026-10-18). This change builds the probe, the checks and the ledger rows. It changes none of their words. The `PUT` route of decision 29 is the change `agent-connect-route`.
- ADR 72 decision 1: the probe is a durable operation. ADR 120 decision 20: the ledger key rule applies to the probe rows.
- ADR 17: each route declares its action and object (`server:manage-agent` on `@server`).
- ADR 250 and ADR 364: the layers table in design.md, no new lint rule.

Open questions: none
