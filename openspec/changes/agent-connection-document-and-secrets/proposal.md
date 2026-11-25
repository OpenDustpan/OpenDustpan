---
version: 2
---

## Why

ADR 129 (accepted 2026-10-18, owner) says: "The operator sets every field of the model connection." The model connection is one JSON document, `agent.ConnectionSpec` (decision 2), and its credential is a secret item of the scope `@server` (decisions 9 and 10). Main has none of this. `internal/agent/settings.go:15` has an older `Connection` type with the fields `Adapter`, `BaseURL`, `Model` and `MaxOutputTokens`, and `SettingsStore.PutConnection` (`internal/agent/settings.go:115`) has no caller and no SQL adapter. No route reads, writes or deletes a connection. The later changes of the connect chain (model adapters, probe, connect route) need the document, its validation, the secret items and the URL check first.

## What Changes

- The type `agent.ConnectionSpec` with the 18 fields of decision 3, in version 1, and its `Validate`. It refuses an unknown field, a document above 32 KiB, a string with a control character, and a `format` of `responses` with `ErrFormatNotSupported` (decisions 2 and 5).
- The field rules that need no model call: `baseURL` (decision 4), `auth` (decision 9), `headers` and `query` (decision 11, the name rules and the plain-value check by name), `userAgent` (decision 12), `outputTokens` and `tools` (decisions 13 and 14), `transport`, `reasoning`, `cache`, `conversationKey`, `providerFields`, `errors`, `rateLimit` and `requestIDHeaders` as the structural rules of decisions 15 to 23. The adapter behavior of those fields is not in this change.
- The secret items of the scope `@server`: the NEW port `agent.SecretItems` and its routes `POST`, `GET`, `PUT` and `DELETE` under `/api/agent/secrets` (decision 10), sealed with `secret.InstallationSecrets`. A reference row with a foreign key makes a `DELETE` of a used item answer 409.
- The stored connection: the tables for the current document, its version and its reference rows, and the pending document with the key of a probe ID (decision 29), as storage only. The probe and the route that starts it are not in this change.
- `egress.CheckModelURL` and the egress allowlist setting with `GET` and `PUT /api/server/egress-allowlist` (decisions 25 and 26), and the flag `--egress-public-only`.
- The routes `GET /api/agent/connection`, `DELETE /api/agent/connection` and `GET /api/agent/formats` (decision 32). `GET` answers the document with references only, its version, the pending document and the result of the last probe. `DELETE` removes the connection and each pending document in one transaction (decision 29).
- The audit entries of decision 33 for a disconnect, a secret item and an allowlist change.

## Out of scope

- `PUT /api/agent/connection` and the capability probe (decisions 29 to 31). They are the changes `agent-connection-probe` and `agent-connect-route` of the plan. The storage of the pending document is in this change so that those changes add no table.
- The model adapters and the dial guard on the connection of a call (decisions 8, 27 and 28): the change `agent-model-adapters`.
- `agent.ConnectModel` and the creation of Dusty (the change `dusty-agent-principal-on-model-connect`).
- The secret filter of decision 24. Main has no `agent.SecretFilter` and no change in `openspec/changes/` builds it. The plain-value check of decision 11 that reads a value for secret-like text is not in this change. It stays out until the filter is on main. The check by the header or parameter name is in this change.
- The console form, the managed connection (decision 34) and the reading of a version other than 1.

## Depends on

- Main: `secret.InstallationSecrets` (`pkg/secret/installation.go:78`), `pkg/egress` (`pkg/egress/egress.go`), `ActionServerManageAgent` (`pkg/identity/identity.go:541`) and `ActionServerManageSettings` (`pkg/identity/identity.go:280`).
- Nothing in `openspec/changes/`. This is the first change of the connect chain.

## Usability

The persona is the administrator who sets up Dusty. The administrator stores a key with `POST /api/agent/secrets`, gets the reference `itm-...`, and writes the document. Every error says what failed and the one command that helps. Example: "The header x-api-key has a plain value. Store the value with POST /api/agent/secrets and send the reference." The administrator reads the effective document with `GET /api/agent/connection` and sees the references, the version and the audit entry. No default model or provider exists (decision 1), so the docs page shows generic worked examples.

## What main already does

- `internal/agent/settings.go:15`: the older `Connection` type. `internal/agent/settings.go:115`: `PutConnection`, no caller, no SQL adapter (`rg PutConnection internal pkg` finds the port and one test fake).
- `pkg/secret/installation.go:78`: `InstallationSecrets` with `Seal` and `Open`.
- `pkg/egress/egress.go:278`: `CheckURL`. `pkg/egress/egress.go:124`: `ParseAllowedRanges`. There is no `CheckModelURL` and no allowlist of host names.
- `pkg/identity/identity.go:541`: `ActionServerManageAgent`. No route uses it for the connection.
- No `pkg/modeladapter`, no `agent.ConnectionSpec`, no route under `/api/agent/`.

## ADR check

- ADR 129 decisions 1 to 4, 9 to 12, 25, 26, 32 and 33 (owner, 2026-10-18). This change builds them and changes none of their words. Decision 29 (the 202 and the probe) is not touched: the route is a later change.
- ADR 120 decision 7 (the older connection routes) is replaced by ADR 129 decision 32. This change follows ADR 129.
- ADR 23 (sealing with a data key) and ADR 145 decision 13 (secret items of a sink): the secret items follow the same sealing.
- ADR 17: each route declares its action and object. ADR 250 and ADR 364: the layers table in design.md, no new lint rule.
- ADR 362: the references to secret items are stable IDs.

## Open questions

none
