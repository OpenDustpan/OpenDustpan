---
version: 2
---

## Why

ADR 129 (accepted 2026-10-18, owner) says: "One adapter serves every server of its format." The model port of ADR 121 has no adapter on main: there is no `pkg/modeladapter`, and `agent.ConnectionSpec` is built by the change `agent-connection-document-and-secrets`. Without an adapter, a connection document is only data. The probe and the connect route cannot call a model. ADR 129 decision 24 also asks for an always-on secret filter, and no change builds it.

## What Changes

- Two adapters, each in its own package: `pkg/modeladapter/chatcompletions` and `pkg/modeladapter/messages` (decisions 5 and 8). Each uses `net/http` and `encoding/json` only. No adapter has a code path for the name of a provider or a host (decision 8).
- The request rules of decisions 13 to 17: output token field, `tool_choice` and parallel tool calls, `whole` and `stream` transport, reasoning parts, cache modes.
- The conversation key of decisions 18 to 20: the HMAC key of the installation (one row, sealed, made at start), the key for each conversation, no end-user field.
- `providerFields` with the deep merge and the owned-path check (decision 21).
- The error mapping, the request ID headers and the rate limit wait (decisions 22 and 23).
- The dial guard, no proxy, no redirect, and the limits of a call (decisions 27 and 28).
- The changes to the model port (decision 37): the part kind `opaque`, `ErrModelQuota`, and the stop reasons `model-quota`, `model-rate-limited` and `connection-changed`, with the cases that `agenttest.RunModel` gets.
- The adapter of the port for a connection: `agent.NewModel(spec ConnectionSpec, open func(SecretRef) ([]byte, error), clock, logger)` that returns the model of ADR 121.

## Out of scope

- The document, its validation and the secret items: the change `agent-connection-document-and-secrets`.
- `agent.SecretFilter` (decision 24), its test corpus and its wiring: the change `agent-secret-filter`.
- The probe (decisions 29 to 31): the change `agent-connection-probe`. The route: the change `agent-connect-route`.
- The format `responses` (decision 5: a later change).
- Request signing and token exchange (decision 7). A gateway does them.
- The managed connection (decision 34).

## Depends on

- `agent-connection-document-and-secrets` (the type `ConnectionSpec`, `SecretItems.Open`, `egress.CheckModelURL`).
- `agent-secret-filter` (the answer of an adapter is the input of the filter; this change uses no filter code).
- Main: the model port of ADR 121 (`internal/agent`), `pkg/egress`.

## Usability

The persona is the administrator who uses a model server that the project does not know. The administrator sets `format` and the fields of the table. The adapter acts only on those fields, so a difference between servers is a field and not a code release. A refusal says what failed and which field fixes it. Example: "The server refused the field max_tokens. Set outputTokens.field to the name that the server documents." The docs page gives generic examples for a local server, a gateway and a hosted API, with no default.

## What main already does

- The model port and `agenttest.RunModel` of ADR 121 in `internal/agent`. They have a text part, a tool call part and a tool result part. They have no `opaque` part and no `ErrModelQuota`.
- `pkg/egress/egress.go`: `CheckAddr`, `CheckURL`, `ParseAllowedRanges`, `CheckAllowedRange`. No dial guard for a model connection.
- No `pkg/modeladapter`.

## ADR check

- ADR 129 decisions 5 to 8, 13 to 24, 27, 28 and 37 (owner, 2026-10-18). This change builds them and changes none of their words.
- ADR 121 decisions 4, 6 and 8: the sentinels, no stream in the port, and `net/http` and `encoding/json` only.
- ADR 120 decisions 16, 20 and 23: the output token budget, the token ledger and the stable system prompt.
- ADR 124 decision 9: a revealed value stays masked in the transcript.
- ADR 23: the HMAC key is sealed in the scope `@server`.
- ADR 250 and ADR 364: the layers table in design.md, no new lint rule.

## Open questions

none
