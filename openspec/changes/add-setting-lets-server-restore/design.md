## Context

Restore must not replace the access tables by default. An installation setting widens server:restore only when a caller turns it on through the settings API, with each change on record and the two-path guard intact.

## Decisions

1. **API-only key in generic settings.** The setting is a stored installation boolean, default false, with no flag, env or file layer. The change path is PATCH /api/dustpan/v1/settings, gated on server:manage-settings plus server:restore-access with a fresh session step. This writer rule is provisional until the owner reviews it.
2. **Snapshot read at apply time.** The restore apply step reads the stored value once at the start of the apply transaction. A read failure or unknown text fails closed. The two-path check runs in the same write transaction.

## Structure

An unexported restoreAccessSetting value made only by a constructor that maps empty to false and accepts only true or false. Any other text returns an error and the caller fails closed.

## Risks

- An operator turns the setting on and forgets it.
- A wide restore path stays open while the setting is on.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| settings API | Both actions plus fresh session step | Change with an access token only gets 403 |
| restore service | Snapshot value, fail closed | Stored text maybe leaves the access tables unchanged |
| store transaction | Two-path count in the write transaction | Concurrent disable gets 409 |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Added latency of the setting read at restore start | 5 | milliseconds at p95 | one restore apply |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph client[Caller boundary]
 A[Site administrator]
 end
 subgraph server[Server boundary]
 B[Settings API]
 C[Restore service]
 end
 subgraph data[Store boundary]
 D[(Settings and access tables)]
 end
 A --> B
 A --> C
 B --> D
 C --> D
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Caller boundary | E | A caller with only server:restore turns the setting on to widen restore. | boundary check | Settings API accepts only the values true or false for the key | PATCH with the value maybe gets 400 settings.value-invalid |
| Caller boundary | E | A caller with only server:restore turns the setting on to widen restore. | encode or contain | The write goes through a typed constructor and a placeholder query | A value with SQL text is refused and stored nowhere |
| Caller boundary | E | A caller with only server:restore turns the setting on to widen restore. | limit | The key has no flag, env or file layer; only the API writes it | An env var that sets the key has no effect on a restore |
| Caller boundary | E | A caller with only server:restore turns the setting on to widen restore. | authorize at point of use | Settings API needs both actions and a fresh session step | A caller with only server:restore gets 403 on the change |
| Caller boundary | E | A caller with only server:restore turns the setting on to widen restore. | fail closed | A failed authorization check denies the change | A failing authorizer leaves the value unchanged |
| Store boundary | T | A stored value of unknown text widens restore. | boundary check | Constructor accepts only true or false | Stored text maybe is refused by the constructor |
| Store boundary | T | A stored value of unknown text widens restore. | encode or contain | The restore service reads the value once into the typed value | A change after the read does not alter the running apply |
| Store boundary | T | A stored value of unknown text widens restore. | limit | The setting only widens server:restore for the access tables, never other actions | With the setting on, a caller without server:restore is still refused |
| Store boundary | T | A stored value of unknown text widens restore. | authorize at point of use | The restore route checks server:restore at the apply | A caller without server:restore gets 403 with the setting on |
| Store boundary | T | A stored value of unknown text widens restore. | fail closed | Unknown text or a read failure keeps the access tables unchanged | Stored text maybe refuses the access-table part |
| Server boundary | R | A change or widened restore leaves no record. | boundary check | The audit entry needs the old and the new value | An entry with a missing value is refused |
| Server boundary | R | A change or widened restore leaves no record. | encode or contain | The entry holds values as typed fields, not free text | CR and LF in a value are not in the log line |
| Server boundary | R | A change or widened restore leaves no record. | authorize at point of use | Reading the audit entry needs the audit read action | A caller without it gets 403 on the entry |
| Server boundary | R | A change or widened restore leaves no record. | fail closed | The change and its audit entry commit in one transaction | A failed audit write leaves the value unchanged |
