## Context

A probe turns a connection document into a result that an operator can read. It runs as a durable operation so that no route waits on a model. It uses the adapter of the document, so it tests the same code path that a run uses.

## Decisions

1. The probe is the operation kind `agent.probe`. One step runs one check. The operation holds the probe ID, the document source (a pending document ID or an inline document) and the `If-Match` version. A step is idempotent: a second run of a step with the same key writes no second ledger row.
2. An inline document (the `POST` route) is held in the operation payload, sealed as the other operation payloads are. It is not stored as a connection and holds no pending row. It holds only references. The probe opens each secret at the call.
3. The probe checks only the document of its own probe ID (decision 29). A step reads the pending document by ID, and the promote step compares the `If-Match` version in the same transaction as the write.
4. Each check has its timeout: 2 minutes, `reach` 10 seconds. The probe has a total of 10 minutes. A timeout fails the check with the sentinel `ErrProbeTimeout`.
5. `reach` opens a TCP connection and a TLS handshake for `https`, with the dial guard. It sends no HTTP request. The other checks use `agent.NewModel` of the document.
6. Each probe call sends the lower of `outputTokens.max` and 1,024 as the output token limit. A full probe makes at most 7 calls.
7. The error text of a failure is a fixed text for each check and cause, written in strict STE. It names the field to look at. It never holds a byte of the answer of the server. The status class is `4xx`, `5xx` or `network`.
8. The ledger row of a call has the origin `probe`, no run, and the key probe ID, check name and attempt number. A second row with the same key changes nothing.
9. The start limit is a counter in the store: 6 starts in a sliding minute for the installation. The count and the start are one transaction, so two instances cannot start 7. A start above the limit answers 429 with `retry-after`.
10. The cancel is a state of the probe row. A step reads the state at its start and ends with `canceled` when it is set. A canceled probe's last step finds no pending document and changes nothing.
11. The audit entry of a probe with no store holds the result of each check and no value, no prompt, no answer and no error text of the server (decision 33).

## Structure

```go
// internal/agent/probe.go
type ProbeID string
type CheckName string // reach, auth, tools, tool-result, transport, usage, reasoning, cache
type CheckState string // pending, running, passed, warning, failed, skipped, canceled

type ProbeResult struct {
    ID     ProbeID
    State  CheckState
    Checks []CheckResult
}
type CheckResult struct {
    Name        CheckName
    State       CheckState
    Took        time.Duration
    InputTokens, OutputTokens int
    StatusClass string // "4xx", "5xx", "network", ""
    Sentinel    string
    Message     string // strict STE, fixed text
}

type Probes interface {
    Start(ctx context.Context, in ProbeInput) (ProbeID, error) // ErrProbeRateLimited{RetryAfter}
    Get(ctx context.Context, id ProbeID) (ProbeResult, error)  // ErrProbeNotFound
}

var (
    ErrProbeRateLimited = errors.New("agent: probe rate limited")
    ErrProbeNotFound    = errors.New("agent: probe not found")
    ErrProbeTimeout     = errors.New("agent: probe timeout")
    ErrProbeStale       = errors.New("agent: connection version changed")
)
```

Packages: `internal/agent` (types and the port), `internal/agent/agenttest` (conformance suite `ProbesSuite`), `internal/agentsvc` (the steps), `internal/store/{sqlite,postgres}` (probe rows, ledger rows, the counter), `internal/api/server` (two routes).

## Contract

| Route | Action | Object | Answer |
|---|---|---|---|
| `POST /api/agent/connection/probe` | `server:manage-agent` | `@server` | 202 `{probeId}`; 429 with `retry-after`; 400 for an invalid document |
| `GET /api/agent/connection/probes/{id}` | `server:manage-agent` | `@server` | 200 `ProbeResult`; 404 |

Error codes are in `internal/errcode/testdata/catalog/agent-probe.txt`.

## Risks

- A model with no tool calls fails the `tools` check. This is the intended result. The message says to turn on tool calls.
- The `cache` check needs a prefix of 2,048 tokens. It costs more input tokens than the other checks. The total stays near 6,000 input tokens.
- A step that crashes after the call and before the ledger row repeats the call. The ledger key makes the second row a no-op, and the cost is one call.

## Layers (defense in depth)

| Input | Layer | Guard | Hostile test (fails when the guard is removed) |
|---|---|---|---|
| document of `POST` | Boundary | Same parse and size limit as the connection document | `TestProbeRoute_HostileDocument` |
| server answer | Boundary | A strict decoder; an answer above the limit fails the check | `TestProbe_OversizeAnswer` |
| server error text | Output | The message is a fixed text; the answer never reaches the result, a log or an audit entry | `TestProbe_ErrorTextNeverInResult` (the server answers a canary secret in its error body) |
| probe call | Use | Dial guard and egress check; the `reach` check uses them | `TestProbe_Reach_DeniedAddress` |
| probe cost | Limit | Output token cap of 1,024; at most 7 calls | `TestProbe_TokenCap` |
| start rate | Limit | 6 starts in a minute, counted in the store transaction | `TestProbeStart_RateLimit_TwoInstances` |
| stale version | Store | The promote step compares `If-Match` in its transaction | `TestProbe_Promote_StaleVersion` |
| canceled probe | Store | A step reads the cancel state; the last step changes nothing | `TestProbe_Canceled_NoWrite` |
| ledger row | Store | The key of probe, check and attempt; a repeat changes nothing | `TestProbe_LedgerRow_Idempotent` |
| credential | Use | The step opens the secret at the call; no value in a log, an error or an audit entry | `TestProbe_NoValueInOutput` |
| route | Authorization | `server:manage-agent` at the handler, for `@server` | `TestProbeRoutes_Forbidden` |
| every guard | Fail closed | An error or an unknown state denies and writes an audit entry | `TestFailClosed_Table` |

Each check that looks redundant gets the comment: "Defense in depth: the route and the store check this too; this check stays so that a new caller cannot skip it."
