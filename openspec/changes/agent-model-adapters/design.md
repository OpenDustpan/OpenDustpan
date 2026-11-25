## Context

The connection document of ADR 129 names a format and the fields that differ between servers. An adapter turns the model port of ADR 121 into HTTP calls for one format. This change builds the two first adapters, the guard on their connections, and the secret filter that the whole agent path needs.

## Decisions

1. Each format is one package: `pkg/modeladapter/chatcompletions` and `pkg/modeladapter/messages`. Both import `net/http` and `encoding/json` only. Both are outside the domain packages of the depguard rule, as ADR 129 decision 8 says.
2. `agent.NewModel` picks the adapter by `spec.Format()` from a fixed map. An unknown format fails closed with `ErrFormatNotSupported`. No code path names a provider or a host.
3. The request body is built as an ordered structure and written in canonical JSON, so the bytes of the tool catalog and the prefix stay the same between requests (decisions 14 and 20).
4. `providerFields` is merged by the rule of decision 21, after the adapter writes its own fields. The owned paths are a fixed list in `pkg/modeladapter/owned.go`. The validation function `OwnedPathConflict(spec)` is called by `ParseConnectionSpec` in the change `agent-connection-document-and-secrets`, so this check has one source. If that change merged first, this change moves the call there in its task 2.1.
5. The conversation key follows decision 19: the first 26 characters of the lower-case base32 of HMAC-SHA256 over the ID of the first run. The HMAC key is a row in a table with one row and a unique key, inserted with `ON CONFLICT DO NOTHING` and then read, at start and before any route is served. No route reads or rotates it.
6. The dial guard is a `net.Dialer.Control` function on a client that the adapter owns. It checks each resolved address: the always-denied list first, then a public address or a CIDR entry on its port (decision 27). The client has `Proxy: nil`, `CheckRedirect` that returns `http.ErrUseLastResponse`, a call timeout of 10 minutes, a stream idle timeout of 2 minutes and a body limit of 4 MiB (decision 28). The client closes idle connections when the allowlist changes.
7. The error mapping reads `error.type` and `error.code` of the body and never its text. The table of decision 22 is data in `errors.go`, and a table test covers each row.
8. The rate limit wait parses the four forms of decision 23 with one function and the clock of the adapter. A wait above 5 minutes returns `ErrModelRateLimited` with the wait, and the loop stops the run with `model-rate-limited`.
9. The adapters hold no secret filter. The filter is the change `agent-secret-filter` (ADR 129 decision 24). An adapter never logs or returns a secret value, and the answer of an adapter is the input of the filter.
10. The opaque part (decision 37) is a new part kind with the format name and the bytes. The store seals it with the run, turn and part as additional data. The filter of the later change never changes it. The transcript record of an opaque part holds its size and format only.

## Structure

```go
// internal/agent/model.go (additions)
type PartOpaque struct { Format string; Bytes []byte }

var ErrModelQuota = errors.New("agent: model quota")

const (
    StopModelQuota        StopReason = "model-quota"
    StopModelRateLimited  StopReason = "model-rate-limited"
    StopConnectionChanged StopReason = "connection-changed"
)

// internal/agent/connect.go
func NewModel(spec ConnectionSpec, open func(context.Context, SecretRef) ([]byte, error),
    guard egress.DialGuard, clk clock.Clock, log *slog.Logger) (Model, error)

// pkg/egress
type DialGuard interface { Control(network, address string, c syscall.RawConn) error }
func NewDialGuard(allow Allowlist, publicOnly bool) DialGuard
```

## Contract

No new route. The adapters send HTTP requests to the base address of the document. The answers map to the model port as decision 22 says. The token use, the cache read tokens and the cache write tokens of each call go to the ledger (ADR 120 decision 20).

## Risks

- A server may accept a request that the adapter built and still behave in a way that the format does not say. The probe of a later change shows this; the adapters do not guess.
- DNS rebinding of an allowed host name to an allowed CIDR is accepted by decision 27, with its reasons.

## Layers (defense in depth)

| Input | Layer | Guard | Hostile test (fails when the guard is removed) |
|---|---|---|---|
| model answer body | Boundary | Body limit 4 MiB, strict decode, unknown stop value fails closed | `TestAdapter_OversizeAnswer`, `TestAdapter_UnknownStop` |
| opaque part | Store | Sealed with additional data; the readable record holds size and format only | `TestOpaque_RoundTripByteForByte`, `TestOpaque_RecordHoldsSizeAndFormatOnly` |
| `providerFields` | Boundary | Owned-path check at each level | `TestProviderFields_OwnedPath_Table` (each owned path, a value that is not an object above an owned path) |
| `providerFields` | Use | The deep merge never writes over an adapter value | `TestMerge_NeverOverwritesOwned` |
| end-user field | Boundary and use | `user`, `safety_identifier`, `metadata.user_id` and subject headers refused; adapter never sends one | `TestNoEndUserField` |
| connection address | Use | Dial guard on each resolved address; always-denied list first | `TestDialGuard_DeniedAddress_Table`, `TestDialGuard_Rebinding` |
| redirect and proxy | Use | `ErrUseLastResponse`; `Proxy: nil` with `HTTP_PROXY` set in the test | `TestClient_NoRedirect`, `TestClient_IgnoresProxyEnv` |
| call time | Limit | 10 minute call, 2 minute stream idle | `TestClient_StreamIdleTimeout` |
| error body | Boundary | Mapping reads `error.type` and `error.code` only | `TestErrorMapping_Table`, `TestErrorMapping_IgnoresText` |
| rate limit wait | Boundary | Four forms parsed; a wait above 5 minutes stops the run | `TestWait_Forms_Table`, `TestWait_AboveLimit` |
| conversation key | Boundary | No run ID, subject, namespace or name in the key | `TestConversationKey_NoIdentity` |
| HMAC key | Store | One row, unique key, never answered by a route | `TestHMACKey_TwoInstances`, `TestHMACKey_NoRoute` |
| credential | Use | Value read at the call, set in the header, never in a log or error | `TestAdapter_NoValueInLogOrError` |
| every guard | Fail closed | An error or an unknown value denies and writes an audit entry | `TestFailClosed_Table` |

Each check that looks redundant gets the comment: "Defense in depth: the validation of the document checks this too; this check stays so that a new caller cannot skip it."
