## Context

The exchange is security code on an external TLS endpoint. It fails closed.

## Decisions

1. **Verified chain value only.** The service takes an opaque verified-chain value from the TLS layer and never reads headers for a chain.
2. **Single enabled rule match.** Match picks at most 1 enabled rule for each exchange call, by bundle ID and exact SPIFFE ID; 0 or 2 or more matches refuse.
3. **Stored issue.** Match check and issue share one transaction. Revoke uses the service-token revoke routes.
4. **Own kind.** The token has the kind `mtls-exchange` (ADR 371 decision 2). The constant `KindMTLSExchange` (NEW) sits next to the other kinds in `pkg/accesstoken`. The credential stays `trust-token`.

5. **Store matches again.** `trustrule.Exchange` gets the field `SPIFFEID`. For a token of the kind `mtls-exchange` the adapter reads the rules of the fence namespace in the `RecordExchange` transaction, and refuses unless exactly one rule names the SPIFFE ID in the bundle of the rule of the exchange and that rule is the rule of the exchange. The JWT exchange sets `SPIFFEID` to empty. A rule added after the service read its rules still refuses. The write already carries the namespace fence: the exchange writes a token and an audit entry, so it uses the fence of `Namespaces.WritableHere`, and no new write path exists.
6. **One route.** The route stays `POST .../trust-rules/exchange`, on the public scope. A call with an empty body is an mTLS call. A call with a body is the JWT exchange, unchanged. The listener setting that requests a client certificate is part 3; until then no certificate reaches the handler and an mTLS call gets 401 mtls.no-client-certificate.
7. **Revocation is an injected check.** NEW `VerifyChain` takes a check function. A nil check gives ErrRevocationUnknown and no token. The source of revocation data is outside this change.

8. **Place and review.** The mTLS service sits in `internal/trustrulesvc` next to the JWT exchange, as the service of the port in `pkg/trustrule`. The work lands in two commits: (a) the trustrule port, its conformance cases and both store adapters; (b) the service, the handler, the error codes and the docs. The Opus security review (rev2) covers the exact head of each commit.

## Structure

Unexported fields of MtlsExchanger with a constructor that takes the stores, the issuer, the clock and the policy and refuses nil values.

## Risks

- Two rules with the same SPIFFE ID refuse.
- A large chain or high rate loads the verifier.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary check | The TLS adapter checks chain, time, revocation, depth 8 and size 32 KB. | Send another CA, expired, revoked and oversize chains; each refuses. |
| authorize at point of use | The service rechecks rule, bundle, principal and namespace in one transaction. | Send a forged forwarded-chain header, disabled rule and cross-namespace principal; each refuses. |
| limit | The handler caps body size and rate and fails closed. | Flood with no-certificate calls and force a store error; no token. |

A `VerifiedChain` has unexported fields, and only `VerifyChain` makes one with a leaf. The kinds of an exchange stay in the one set of `Kind.FromExchange`.

## Diagrams

After this change:

```mermaid
sequenceDiagram
 Workload->>Server: TLS client certificate
 Server->>Server: verify and match
 Server-->>Workload: trust token
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| correctness | 0 tokens issued for refused chains | tokens | mTLS exchange |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph Outside
 W[Workload]
 end
 subgraph Server
 T[TLS exchange route]
 V[Verifier]
 S[Store]
 end
 W --> T --> V --> S
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| TLS exchange route | S | A spoofed certificate or forwarded-chain header gains a trust token. | boundary check | Verify chain, time and revocation. | Replay other CA, expired and revoked chains. |
| TLS exchange route | S | A spoofed certificate or forwarded-chain header gains a trust token. | encode or contain | Read the chain only from the TLS session; ignore headers. | Send a forged forwarded-chain header; it refuses. |
| TLS exchange route | S | A spoofed certificate or forwarded-chain header gains a trust token. | limit | Cap chain size, body and rate. | Flood with oversize chains. |
| TLS exchange route | S | A spoofed certificate or forwarded-chain header gains a trust token. | authorize at point of use | Single enabled rule and in-namespace enabled principal. | Replay duplicate rules and cross-namespace principal. |
| TLS exchange route | S | A spoofed certificate or forwarded-chain header gains a trust token. | fail closed | Any error gives no token. | Force a store error; no token. |

## Contract

Ports and types (Go signatures; no function body):

- `pkg/accesstoken`: `const KindMTLSExchange Kind = "mtls-exchange"`; `Kind.Valid` and `Kind.FromExchange` accept it. `type MtlsInput struct { At, LeafNotAfter time.Time; PrincipalID, Name, CreatedBy string }`. `func (s *Service) NewMtlsTrustToken(ctx context.Context, in MtlsInput) (Issued, error)` returns a token that expires at the earlier of `At` plus the exchange lifetime and `LeafNotAfter`. It returns `ErrInvalid` for a bad name, a zero or past `LeafNotAfter`, or a lifetime that is not positive; it does not store the token.
- `pkg/trustrule`: `type Exchange struct { Token accesstoken.Token; Digest accesstoken.Digest; Rule ID; SPIFFEID string }`. `ExchangeStore.RecordExchange(ctx, x Exchange, fence namespace.Fence, e audit.Entry) error` keeps its signature. New rule: for `mtls-exchange`, `SPIFFEID` is not empty and has no `*`; for `jwt-exchange` it is empty; otherwise `ErrInvalid`. A rule that is not the one rule that names `SPIFFEID` in its bundle gives `ErrNotFound`. Other sentinels are unchanged: `ErrPrincipal`, `accesstoken.ErrAlreadyExists`, `namespace.ErrFenced`, `audit.ErrInvalidEntry`.
- `pkg/store/internal/trustrulestore`: `func CheckExchangeMatch(rows []Row, x *trustrule.Exchange) error` returns `ErrNotFound` unless exactly one row names `x.SPIFFEID` in the bundle of the row of `x.Rule` and it is that row. `CheckExchange` accepts both kinds.
- `internal/trustrulesvc` (existing package, NEW files mtls_exchange.go and mtls_exchange_limit.go; the JWT `Exchanger`, `ExchangeConfig`, `ExchangeInput`, `ExchangeResult`, `TokenIssuer` and `ActionExchange` keep their names, and the mTLS code reuses `ActionExchange`): `func VerifyChain(rawChain [][]byte, roots *x509.CertPool, now time.Time, check func(*x509.Certificate) error) (VerifiedChain, error)`; `func MatchRule(rules []trustrule.Rule, ns namespace.Name, spiffeID, bundleID string) (trustrule.Rule, error)`; `func NewMtlsExchanger(cfg MtlsConfig) (*MtlsExchanger, error)`; `func (e *MtlsExchanger) Verify(ctx, *MtlsVerifyInput) (VerifiedChain, error)`; `func (e *MtlsExchanger) Exchange(ctx, *MtlsExchangeInput) (MtlsExchangeResult, error)`; `func NewMtlsGuard(cfg MtlsGuardConfig) (*MtlsGuard, error)`; `type MtlsTokenIssuer interface`. Sentinels: ErrNoCertificate, ErrUnknownIssuer, ErrExpired, ErrNotYetValid, ErrRevoked, ErrRevocationUnknown, ErrNoMatchingRule, ErrAmbiguousRule, ErrBundleUnknown, ErrInvalid, ErrIssueFailed, ErrRateLimited, ErrMissingDependency. Bounds: `MaxChainBytes` 32768, `MaxChainDepth` 8. The leaf time check has no skew.

API: `POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange`, public scope (no sign-in, no authorization action; the route stays on the public list of the route test). Request: an empty body, and a client certificate in the TLS handshake. Response 201: `{"expiresAt": RFC 3339 UTC, "token": secret, "id": token ID, "principal": principal ID, "rule": rule ID}`, header `Cache-Control: no-store`. No paging, no ETag. The audit entry has the action `trust-rule:exchange`, the object of the namespace, the keys principal, kind, spiffeID and expiresAt, and the fence of the namespace. Each refusal writes a denial entry with outcome denied.

| Status | errorCode | Sentinel |
| --- | --- | --- |
| 401 | mtls.no-client-certificate | ErrNoCertificate |
| 401 | mtls.unknown-issuer | ErrUnknownIssuer, and ErrInvalid for an oversize, too deep or unparsable chain |
| 401 | mtls.expired | ErrExpired |
| 401 | mtls.not-yet-valid | ErrNotYetValid |
| 401 | mtls.revoked | ErrRevoked |
| 401 | mtls.revocation-unknown | ErrRevocationUnknown |
| 401 | mtls.no-matching-rule | ErrNoMatchingRule, and trustrule.ErrNotFound from the store |
| 401 | mtls.ambiguous-rule | ErrAmbiguousRule |
| 401 | mtls.bundle-disabled | ErrBundleUnknown (no bundle, or no active version) |
| 401 | mtls.principal-disabled | trustrule.ErrPrincipal (not found, not a service principal, other namespace, or disabled) |
| 413 | request.body-too-large | body over `MaxBodyBytes` (existing code) |
| 429 | mtls.rate-limited | ErrRateLimited |
| 500 | token.issue-failed | ErrIssueFailed: no token and no entry |

A call with the forged header of a forwarded chain and no handshake certificate gives 401 mtls.no-client-certificate. Out of scope: the listener setting that requests the client certificate, the e2e step and the docs page (part 3), the source of revocation data, JWT behavior.

Error codes: NEW mtls.* in `internal/errcode/catalog_mtls.go`; NEW token.issue-failed in `internal/errcode/catalog_token.go`; the golden lists of the codes are written by hand; the API description and the Go client golden are generated.

## Contracts

**trust-token**

- Invariant: One live token names one namespace, one rule and one non-person principal. Its kind is `mtls-exchange`.
- `exchange`: pre: A verified client chain is present.. Post: A stored token with an audit entry exists, or no token exists..

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Verify revocation | Responder slow or unreachable | Retry the exchange after the cache TTL. | No token. |
| Issue | Store fails | Retry as a new call. | No token and no audit entry. |
