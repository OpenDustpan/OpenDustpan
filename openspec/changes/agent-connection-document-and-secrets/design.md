## Context

ADR 129 defines the model connection as one document that the operator writes, and the credential as a secret item of the scope `@server`. Main has the older `Connection` type and a `SettingsStore` port with no adapter and no caller. This change builds the document type, its validation, the secret items, the stored connection (current and pending), the URL check and the read and delete routes. The write route and the probe come later in the chain.

## Decisions

1. `agent.ConnectionSpec` is a new type in `internal/agent`. It has `version: 1` and the 18 fields of ADR 129 decision 3. The decoder uses `DisallowUnknownFields`, a size limit of 32 KiB before the parse, and a check for control characters in each string. The older `Connection` type is removed in the same change, with its port method `PutConnection`, because no caller uses them (rule: no code for a shape that no tag shipped).
2. `ConnectionSpec` has unexported fields and a constructor `agent.ParseConnectionSpec(raw []byte) (ConnectionSpec, error)`. A value of the type exists only after validation. The type is immutable. `Marshal` writes the canonical form.
3. A secret reference is the type `agent.SecretRef`, a stable ID `itm-...`. The document stores references only. `ParseConnectionSpec` checks the form of the reference. The check that the item exists is in the store transaction (layer table).
4. `headers` and `query` use the name rules of ADR 129 decision 11. A name that holds `token`, `key`, `secret`, `auth`, `session`, `cookie`, `password` or `signature` must be a reference to a secret item, else `ErrPlainSecret`. The value half of the check needs `agent.SecretFilter`. Main has no such type, so this change does not do it.
5. `agent.SecretItems` is a NEW port with `Create`, `List`, `Get` (metadata only), `Replace` and `Delete`. It stores the sealed value with `secret.InstallationSecrets`. No method returns a value to a route. A separate method `Open(ctx, ref)` returns the value to the adapter code of a later change and is not on the route layer.
6. `agent.ConnectionStore` is a NEW port: `Current`, `Pending(probeID)`, `PutPending`, `Promote`, `Delete`. This change builds `Current`, `Delete` and the tables. `PutPending` and `Promote` are built here and tested by their conformance suite, and the later changes call them. `Delete` removes the current document, each pending document and the reference rows in one transaction.
7. `egress.CheckModelURL(ctx, rawURL, allow Allowlist)` checks the scheme, the host and the resolved addresses against the always-denied list and the allowlist. `egress.Allowlist` holds host names and CIDR rules. The setting is stored in the installation settings and read with `GET` and `PUT /api/server/egress-allowlist`. The action is `server:manage-settings`.
8. The flag `--egress-public-only` makes the always-denied list stricter: private ranges are denied even when the allowlist has a rule for them. The flag is in `cmd/opendustpan`.
9. The routes carry the action `server:manage-agent` on `@server` (decision 32), except the allowlist routes. Each handler checks the action at the point of use.
10. Audit entries: kind `change` on `@server` for a disconnect, a secret item (create, replace, delete) and an allowlist change. An entry never holds a value.

## Structure

```go
// internal/agent/connection.go
type SecretRef string // "itm-..."

type ConnectionSpec struct { /* unexported fields, 18 fields of the table */ }

func ParseConnectionSpec(raw []byte) (ConnectionSpec, error)
func (c ConnectionSpec) Marshal() []byte
func (c ConnectionSpec) SecretRefs() []SecretRef

var (
    ErrFormatNotSupported = errors.New("agent: format not supported")
    ErrPlainSecret        = errors.New("agent: plain secret in header or query")
    ErrConnectionInvalid  = errors.New("agent: connection document invalid")
)

// internal/agent/secretitems.go
type SecretItems interface {
    Create(ctx context.Context, name string, value []byte) (SecretItem, error)
    List(ctx context.Context) ([]SecretItem, error)
    Get(ctx context.Context, ref SecretRef) (SecretItem, error)
    Replace(ctx context.Context, ref SecretRef, value []byte) (SecretItem, error)
    Delete(ctx context.Context, ref SecretRef) error // ErrSecretInUse while referenced
    Open(ctx context.Context, ref SecretRef) ([]byte, error)
}
```

Packages: `internal/agent` (types, ports, `agenttest` conformance suites), `internal/agentsvc` (service), `internal/store/{sqlite,postgres}` (adapters and tables in the one schema file of each dialect), `internal/api/server` (routes), `pkg/egress` (`CheckModelURL`, `Allowlist`).

## Contract

| Route | Action | Object | Answer |
|---|---|---|---|
| `POST /api/agent/secrets` | `server:manage-agent` | `@server` | 201, `{ref, name, createdAt}` |
| `GET /api/agent/secrets` | `server:manage-agent` | `@server` | 200, list with metadata, no value |
| `PUT /api/agent/secrets/{ref}` | `server:manage-agent` | `@server` | 200, metadata |
| `DELETE /api/agent/secrets/{ref}` | `server:manage-agent` | `@server` | 204, or 409 `secret_in_use` |
| `GET /api/agent/connection` | `server:manage-agent` | `@server` | 200, document with references, `version`, pending document, last probe result; 404 when none |
| `DELETE /api/agent/connection` | `server:manage-agent` | `@server` | 204 |
| `GET /api/agent/formats` | `server:manage-agent` | `@server` | 200, `["chat-completions","messages"]` |
| `GET /api/server/egress-allowlist` | `server:manage-settings` | `@server` | 200 |
| `PUT /api/server/egress-allowlist` | `server:manage-settings` | `@server` | 200 |

The error codes are in the catalog file `internal/errcode/testdata/catalog/agent-connection.txt`. Each message says what failed and the one command that helps.

## Risks

- The tables are in the one schema file of each dialect (no tag shipped, so no migration).
- `PutPending` and `Promote` have no caller in this change. The conformance suite is their test. The later changes add the callers.
- A document field with no adapter yet (`reasoning`, `cache`) is only checked for structure. A later change adds the behavior.

## Layers (defense in depth)

| Input | Layer | Guard | Hostile test (fails when the guard is removed) |
|---|---|---|---|
| connection document | Boundary | Size limit 32 KiB before parse; unknown field; control character; `responses` | `TestParseConnectionSpec_Hostile` (oversize, unknown field, `\u0000` in `model`, `format: responses`) |
| connection document | Handler | The handler reads the body with `textbody` and the same limit | `TestConnectionRoutes_OversizeBody` |
| `baseURL` | Boundary | Absolute, ≤2048 bytes, no userinfo, query, fragment, no format path suffix | `TestBaseURL_Hostile` |
| `baseURL` | Use | `egress.CheckModelURL` checks the scheme, host and resolved addresses; the always-denied list holds loopback, link-local, metadata | `TestCheckModelURL_DeniedRanges` |
| header and query names | Boundary | Token rules, refused names, ≤16 entries, ≤1 KiB; `ErrPlainSecret` for secret-like names | `TestHeaders_PlainSecretByName` |
| secret reference | Store | Foreign key from the reference row to the item; a missing item makes the write fail | `TestConnectionStore_MissingSecretRef` |
| secret value | Store | Sealed with `InstallationSecrets`; the row never holds a plain value | `TestSecretItems_SealedAtRest` |
| secret value | Handler | No route answers a value; the metadata type has no value field | `TestSecretRoutes_NeverAnswerValue` |
| secret delete | Store | Foreign key makes `DELETE` fail while referenced | `TestSecretItems_DeleteInUse` |
| every route | Authorization | `server:manage-agent` checked in the handler for `@server` | `TestAgentRoutes_Forbidden` (a table: each route, a principal without the grant gets 403 and an audit entry) |
| allowlist | Authorization | `server:manage-settings` | `TestEgressAllowlistRoutes_Forbidden` |
| allowlist rule | Boundary | CIDR parse; the always-denied list wins over a rule | `TestAllowlist_CannotAllowDeniedRange` |
| logs and audit | Output | An entry and a log line never hold a value or a header value | `TestAuditEntries_NoValue` |
| every guard | Fail closed | An error or an unknown value denies and writes an audit entry | `TestFailClosed_Table` |

Each check that looks redundant gets the comment: "Defense in depth: the handler and the service check this too; this check stays so that a new caller cannot skip it."
