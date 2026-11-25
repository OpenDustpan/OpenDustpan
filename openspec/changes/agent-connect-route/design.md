## Context

The route is the first write of a connection. Two administrators may send a document at the same time, and a probe takes up to 10 minutes. The version in `If-Match` and the cancel rule keep the result clear: the last `PUT` wins, and a stale probe changes nothing.

## Decisions

1. `PUT /api/agent/connection` has the action `server:manage-agent` on `@server`, checked in the handler and again in the service at the point of use.
2. The body is the document of decision 3. The handler uses the strict decoder and the limits of `ParseConnectionSpec`. An invalid document answers 400 with the name of the field.
3. `If-Match` holds the version of the connection as a quoted integer. No connection has version 0, so the first connect sends `If-Match: "0"`. A missing header answers 428. A header that is not a quoted integer answers 400. A version that is not current answers 412.
4. In one transaction, the service: checks the version, cancels earlier probes that have a pending document, removes those documents, stores the new pending document with the new probe ID, and writes the probe row. The start of the probe (a durable operation) is in the same transaction.
5. The start limit of decision 31 applies. A `PUT` above the limit answers 429 with `retry-after` and stores nothing.
6. The promote step of the probe calls `agent.ConnectModel(ctx, actor, in)`. The `in` holds the pending document ID and the `If-Match` version. In one transaction, `ConnectModel` checks the version, calls `ConnectionStore.Promote`, and does the writes of Dusty. If the version is no longer current, nothing is written and the probe ends `failed`.
7. This change edits one thing of `dusty-agent-principal-on-model-connect`: the connection write of `ConnectModel` is `ConnectionStore.Promote` and not `SettingsStore.PutConnection`, because the change `agent-connection-document-and-secrets` removes the older type.
8. The actor and the creator of Dusty come from the authenticated principal of the `PUT`. The probe operation holds the actor ID, and the promote step loads the principal again at its start. A principal that no longer has the action ends the probe `failed`.
9. The audit entry (kind `change` on `@server`) is written in the transaction of the promote step. It holds the old and new document with references only, and the probe result.

## Structure

```go
// internal/agentsvc/connect.go
func (s *Service) PutConnection(ctx context.Context, actor identity.Principal, doc ConnectionSpec, ifMatch int64) (ProbeID, error)
// errors: ErrConnectionVersion (412), ErrProbeRateLimited (429), ErrInvalidConnection (400)
```

## Contract

| Route | Action | Object | Answer |
|---|---|---|---|
| `PUT /api/agent/connection` | `server:manage-agent` | `@server` | 202 `{probeId}`; 400 invalid document or header; 412 stale version; 428 no `If-Match`; 429 with `retry-after` |

Error codes are in `internal/errcode/testdata/catalog/agent-connect.txt`.

## Risks

- Two instances may handle two `PUT` at once. The transaction and the version check make one of them fail with 412.
- A probe that passes after a `DELETE` finds no pending document and changes nothing.

## Layers (defense in depth)

| Input | Layer | Guard | Hostile test (fails when the guard is removed) |
|---|---|---|---|
| document | Boundary | Strict decode, size limit, field check | `TestPutConnection_HostileDocument_Table` |
| `If-Match` | Boundary | Quoted integer only | `TestPutConnection_IfMatch_Table` (missing, `*`, weak, negative, large) |
| secret value in a field | Boundary | `CheckNoSecretValue` | `TestPutConnection_SecretValueInField` |
| version | Store | The transaction checks it at the start and at the promote step | `TestPutConnection_TwoWriters`, `TestPromote_StaleVersion` |
| earlier probes | Store | The `PUT` cancels them in its transaction | `TestPutConnection_CancelsEarlierProbes` |
| actor | Authorization | Handler and service both check the action; the principal comes from the session | `TestPutConnection_Forbidden`, `TestConnectModel_ActorFromSession` |
| start rate | Limit | 6 starts in a minute | `TestPutConnection_RateLimit` |
| audit | Fail closed | A failed audit write rolls back the promote | `TestPromote_AuditFailureRollsBack` |

Each check that looks redundant gets the comment: "Defense in depth: the handler checks this too; this check stays so that a new caller cannot skip it."
