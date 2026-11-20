## Context

This change holds the own list of an access token. It is split from `ip-allowlists`, which keeps the installation list, the namespace list, the one switch, the actions and the byte limit. Main has no token list (see the evidence in the proposal). The change is parked until the owner answers open question 1.

## Decisions

1. **The token list is a column of the token row.** `access_tokens` gets the column `ip_allowlist` in both baselines, edited in place. No migration and no conversion code exist (`CLAUDE.md`, "Backend rules"). The store reads the list in the same lookup as the digest (`pkg/accesstoken/store.go:36 (GetTokenByDigest)`), so a change applies at the next request on every instance, as the token policy does. The token field is unexported, and a constructor refuses a list that `ParseList` did not make. The list uses `ipallow.List` (`pkg/ipallow/list.go:47,171`), so a list that did not pass the check cannot reach a checker.
2. **The token check is a second step of `admit`.** `admit` runs the installation list before the credential (`pkg/dustpan/ipallow.go:470`). After authentication gives the token ID (`pkg/identity/identity.go:54`), the same function checks the token list. A request with a token that fails authentication reads no token list. A list that does not decode denies and writes a denial entry. The checker replaces its unused namespace slot with a token slot.
3. **One switch.** The checker calls `listsEnabled` of `ip-allowlists` (owner decision 9 of that change). The token list adds no switch.
4. **Limits.** At most 64 entries (`ipallow.MaxEntries`) and 4096 bytes (owner decision 11 of `ip-allowlists`). The column has no check of its own, so the service refuses a longer value before the write.
5. **Who sets the list.** Open question 1. The design below shows option B, the recommendation. No task builds the route before the owner answers.
6. **Structure over checks.** The bug class "a list that did not pass the check opens a scope" is made impossible by decision 1. The remaining guards are fail-closed branches, and each has a mutation test.

## Contract

**Stored schema** (both dialects, baselines edited in place):

| Table | Column | SQLite | PostgreSQL | Notes |
| --- | --- | --- | --- | --- |
| `access_tokens` | `ip_allowlist` | `TEXT NOT NULL DEFAULT ''` | `text DEFAULT ''::text NOT NULL` | The text of `ipallow.EncodeList`. Empty means no list. |

**Port** (`pkg/accesstoken/store.go`, `TokenStore`). The conformance cases are in `pkg/accesstoken/accesstokentest` and run before an adapter has the method:

```go
// SetTokenIPAllowlist replaces the list of the token id; the zero List removes
// it. The audit entry e commits with the change.
SetTokenIPAllowlist(ctx context.Context, id string, l ipallow.List, e audit.Entry) error
```

Errors: `ErrNotFound` (no token), `ErrEnded` (revoked, expired or replaced), `ErrUnknownKind` (stored kind unknown). `Match` from `GetTokenByDigest` and `Token` from `GetToken` carry the list. A stored value that does not decode gives `ipallow.ErrInvalidList` (wrapped) on read, and the caller denies.

**Service** (`pkg/accesstoken/service.go`):

```go
func (s *Service) SetIPAllowlist(ctx context.Context, caller identity.Principal, e audit.Entry, scope Scope, id string, items []string) (ipallow.List, error)
func (s *Service) ClearIPAllowlist(ctx context.Context, caller identity.Principal, e audit.Entry, scope Scope, id string) error
```

Errors: `ipallow.ErrInvalidList` for an empty or bad list (use `ClearIPAllowlist` to opt out, as for a namespace), `ErrNotFound`, `ErrEnded`, `identity.ErrDenied`, `ErrSignInRequired`. Who may call and the step are open question 1.

**Token route**: set by the answer to open question 1. Option B: `PUT` and `DELETE` `/api/dustpan/v1/me/tokens/{id}/ip-allowlist`, body `{"cidrs":[...]}`, action `token:manage-own` (`pkg/identity/identity.go`), wrapped like the other token changes (`internal/api/server/tokens.go:263 (serveTokenChange)`). The token resource shows `ipAllowlist` as a list of CIDR text, `[]` when none (the effective value, "No magic").

**Audit actions**: a denial writes `installation:deny-ip-allowlist` with the scope `token` (`pkg/dustpan/ipallow.go:418`). The entry carries the client address and the token ID. It never names the token in the response. A change of a token list writes `token:set-ip-allowlist` or `token:remove-ip-allowlist`.

## Structure

The checker is the unexported `allowChecker` (`pkg/dustpan/ipallow.go:62,74`). This change gives it a token slot in place of the namespace slot. The token field of `accesstoken.Token` is unexported, and its constructor takes an `ipallow.List`.

## Risks

- A token list that excludes the address of the CI runner blocks that pipeline. The administrator revokes and recreates the token, or clears the list (open question 1).
- A wrong client address behind a proxy blocks valid tokens.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The token store refuses a value that does not decode, and the read denies. | Store `not-a-cidr` straight in the column and check that the request gets 403. |
| service | `SetIPAllowlist` refuses an empty list, 65 entries, a bad entry and a value over 4096 bytes. | Call the service with each and check `ErrInvalidList` or the limit error. |
| service | The checker denies when any opted-in list denies. | Use an address in the token list but outside the installation list and check denial with scope `installation`. Use an address in the installation list but outside the token list and check scope `token`. |
| HTTP handler | The handler answers 403 for the token list after authentication and writes no other entry. | Send a valid token from an address outside its list and check 403, the audit entry and the token last-used time. |
| HTTP handler | The route needs its action and the step. | Call the route with no action, with an access token and with a stale session. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| latency | The token list adds no query to a request: it comes in the digest lookup. | queries added per request | each request with an access token |

## Threat model

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| network to server | E | A stolen token works from any address. | authorize at point of use | The token list is checked on each request after authentication. | Use a valid token from an address outside its list and check 403 scope `token`. |
| network to server | E | A token holder widens its own list. | authorize at point of use | The route needs a console session, and a widening needs the step when it is on (open question 1). | Call the route with the token itself and check 403. |
| network to server | I | The 403 names a token and so leaks that it exists. | encode or contain | The scope is a fixed value, never text of the request. | Deny a token with a known ID and check that the body holds no ID. |
| store to server | T | A restored backup holds a token list that does not decode. | fail closed | `DecodeList` refuses the value, and the request is denied. | Restore a bad value and check 403. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| request | The token list does not decode. | Clear the list of the token, or repair the row. | The server answers 403 for that token and writes a denial entry. |
| list change | The audit entry cannot be written. | Retry the change. | The change does not happen. |
