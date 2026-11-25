---
version: 1
---

## Why

ADR 129 (accepted 2026-10-18, owner) decision 24 says the secret filter is always on. A model may repeat a secret that it saw in a prompt or a tool result, and the run record would then hold it. The older `MaskText` of main masks known revealed values only. It has no filter for secret-like text and no check for the value of a secret item. This is security code, so it is its own change with its own corpus.

## What Changes

- `agent.SecretFilter`: an immutable value made from the values of the secret items of the connection and the pattern list of the binary. `Filter(string) (string, int)` answers the text and the number of removed values (decision 24).
- The pattern list of the binary: prefixes of known token forms, PEM blocks, JWT, `Authorization` values and `password=` pairs. No rule on entropy alone.
- The test corpus: secrets that the filter must remove, and IDs and hashes from state checks that it must keep.
- The wiring: the filter acts on the prompt of a person, each tool result, each model answer (text, tool call arguments and readable reasoning) and each probe error text. It has no off switch: no option, flag or field.
- The replacement text `[removed: secret-like text]`.
- The value check of decision 11: a field of the document that holds the value of a secret item is refused at connect time.
- An opaque part is never changed by the filter.

## Out of scope

- The adapters, the opaque part and the HMAC key: the change `agent-model-adapters`.
- The document and the secret items: the change `agent-connection-document-and-secrets`.
- The probe: the change `agent-connection-probe`. It calls the filter on a failure text that it builds.
- A filter for other output (logs, audit): they never hold a value by rule, and each has its own test.

## Depends on

- `agent-connection-document-and-secrets` (`SecretItems.Open`, `ConnectionSpec`).
- Main: the model port of ADR 121, `internal/agent/mask.go`.

## Usability

The administrator sees `[removed: secret-like text]` in a transcript where a model repeated a secret. The count of removals is in the run record, so the administrator can see that the filter acted and why. No setting turns the filter off. The docs page lists the pattern forms, so an administrator knows what the filter removes.

## What main already does

- `internal/agent/mask.go`: `MaskText` and `RevealedMarker`. No `SecretFilter`, no pattern list.

## ADR check

- ADR 129 decision 24 (owner, 2026-10-18): the filter, its pattern list and "no off switch". Decision 11: the value check. This change builds both and changes none of their words.
- ADR 250 and ADR 364: layers table in design.md, no new lint rule.

Open questions: none
