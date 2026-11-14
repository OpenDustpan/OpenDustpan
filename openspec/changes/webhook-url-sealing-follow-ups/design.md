## Context

Webhook sink URLs are sealed at rest and opened at send time. Four review nits remain.

## Decisions

1. **Refuse at the production boundary.** checkDestination in pkg/notify/seal.go fails with ErrNoURL for userinfo or a scheme outside http and https. The sink handler maps ErrNoURL to sink.invalid. Scheme match is case-insensitive after trim of ASCII whitespace.
2. **Fail closed on corrupt legacy row.** Migration keeps a legacy userinfo row as an empty target. List omits it. Open fails with ErrNoURL. RestoreService.Apply fails with ErrCorrupt, records the NEW error code `backup.corrupt`, and names the sink. No strip of userinfo.

## Structure

Unexported checkDestination in pkg/notify/seal.go stays the one approver of a webhook target. No new tool.

## Risks

- A strict http and https allowlist refuses a sink that an operator expects to work.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary check | checkDestination refuses userinfo and non-http/https scheme. | Open https://user:pass@example.com/hook and ftp://example.com/hook; each fails with ErrNoURL. |
| fail closed | List omits the empty-target row; restore refuses it with ErrCorrupt. | Stage a sink row with an empty target; restore fails and names the sink. |

## Diagrams

After this change:

```mermaid
sequenceDiagram
 participant C as Client
 participant H as Sink handler
 participant S as SinkSealer
 C->>H: PUT sink with webhook URL
 H->>S: OpenDestination
 S-->>H: ErrNoURL
 H-->>C: sink.invalid
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | No latency, storage or rate change; the fix adds one string check. |  |  |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph client[Untrusted client]
 A[Sink API caller]
 end
 subgraph server[Server]
 B[Sink handler]
 C[SinkSealer]
 end
 A --> B --> C
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Sink handler to SinkSealer | I | A URL with userinfo leaks a credential to another host. | boundary check | checkDestination allowlist | Plain and percent-encoded userinfo fail with ErrNoURL |
| Sink handler to SinkSealer | I | A URL with userinfo leaks a credential to another host. | encode or contain | Sealed target holds no userinfo | Planted userinfo secrets fail on open |
| Sink handler to SinkSealer | I | A URL with userinfo leaks a credential to another host. | limit | One URL per sink write | Two URLs in one write are refused |
| Sink handler to SinkSealer | I | A URL with userinfo leaks a credential to another host. | authorize at point of use | Namespace grant on write and test | Test call without the grant is refused |
| Sink handler to SinkSealer | I | A URL with userinfo leaks a credential to another host. | fail closed | Empty target fails on open and restore | Open with empty target fails with ErrNoURL |

## Contracts

**notify.Sink**

- Invariant: Stored webhook target holds no userinfo.
- `SinkSealer.OpenDestination`: pre: Caller passes the stored sealed secrets.. Post: Returns the URL for http or https with no userinfo.; Returns ErrNoURL otherwise..

## Change impact (computed from main)

Tests:

- `pkg/backup/manifest_kind_test.go` names `ErrCorrupt`
- `pkg/backup/portable_unit_settings_test.go` names `ErrCorrupt`
- `pkg/backup/restore_wiring_test.go` names `ErrCorrupt`

ADRs:

- `docs/adr/0281-any-s3-compatible-store-the-database-decides-which-write-wins.md` names `ErrCorrupt`
