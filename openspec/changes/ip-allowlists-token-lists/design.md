## Context

This change holds the own list of an access token. It is split from `ip-allowlists`, which keeps the installation list, the namespace list, the one switch, the actions and the byte limit. Main has no token list (see the evidence in the proposal). Owner decision 8 (20 Nov 2026) answers who sets the list, so the change is ready.

## Decisions

1. **The token list is a column of the token row.** `access_tokens` gets the column `ip_allowlist` in both baselines, edited in place. No migration and no conversion code exist (`CLAUDE.md`, "Backend rules"). The store reads the list in the same lookup as the digest (`pkg/accesstoken/store.go:36 (GetTokenByDigest)`), so a change applies at the next request on every instance, as the token policy does. The token field is unexported, and a constructor refuses a list that `ParseList` did not make. The list uses `ipallow.List` (`pkg/ipallow/list.go:47,171`), so a list that did not pass the check cannot reach a checker.
2. **The token check is a second step of `admit`.** `admit` runs the installation list before the credential (`pkg/dustpan/ipallow.go:470`). After authentication gives the token ID (`pkg/identity/identity.go:54`), the same function checks the token list. A request with a token that fails authentication reads no token list. A list that does not decode denies and writes a denial entry. The checker replaces its unused namespace slot with a token slot.
3. **One switch.** The checker calls `listsEnabled` of `ip-allowlists` (owner decision 9 of that change). The token list adds no switch.
4. **Limits.** At most 64 entries (`ipallow.MaxEntries`) and 4096 bytes (owner decision 11 of `ip-allowlists`). The column has no check of its own, so the service refuses a longer value before the write.
5. **Who sets the list (owner decision 8).** The check of a revoke (`pkg/accesstoken/scope.go:190 (authorizeRevoke)`): for a personal token the maker only (`checkOwner`, scope.go:169), for a service token a holder of `principal:manage-tokens` on its principal (`requireManageTokens`, scope.go:269). A holder of `server:manage-tokens` has no right on a personal list. The change adds no fresh step, as a revoke has none (`Revoke`, `pkg/accesstoken/service.go:338`). It adds no sign-in rule either: `requireSignIn` (scope.go:66) guards a create and a rotation, not a revoke, so a request made with a token may set a list.
6. **One guard: no widening of the own list.** A request made with a token (`caller.TokenID` set, `pkg/identity/identity.go:54`) whose target is that same token ID may only narrow. Narrowing is a new list that is not empty and that `ipallow.List.Covers` (`pkg/ipallow/list.go:136`) accepts against the stored list, or any list when the stored list is empty. A clear of a stored list is a widening. The store method takes a `narrowOnly` flag and compares inside the write transaction, so a concurrent change cannot slip between the check and the write. The service compares first, so the guard has two layers and each has a mutant.
7. **Structure over checks.** The bug class "a list that did not pass the check opens a scope" is made impossible by decision 1. The bug class "a request widens the list that limits it" is not made impossible by a type, because the caller and the target are values of one request: the guard is a fail-closed branch in the service and in the store, and each has a mutation test.

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

Errors: `ipallow.ErrInvalidList` for an empty or bad list (use `ClearIPAllowlist` to opt out, as for a namespace), `ErrNotFound`, `ErrEnded`, `ErrWrongKind`, `identity.ErrDenied`, and the NEW `ErrListWidening` (wraps `identity.ErrDenied`; the caller used the token whose list it changes, and the new list admits an address that the stored list refuses, or clears it). The service never returns `ErrSignInRequired` for these methods.

Who may call, for `scope` (as `authorizeRevoke`): `ScopeOwn` needs the token of the person of `caller` (`ErrNotFound` for another person); `ScopeService` takes a service token or a token of a trust rule and needs `principal:manage-tokens` on its principal (`ErrWrongKind` for a personal token); `ScopePersonal` is refused with `identity.ErrDenied` (the maker only, owner decision 8). A step is never asked.

The store method takes the guard, so a store adapter cannot skip it. Its signature becomes:

```go
// narrowOnly makes the store refuse, in the same transaction, a list that is
// not covered by the stored list, and a clear of a stored list, with
// ErrListWidening.
SetTokenIPAllowlist(ctx context.Context, id string, l ipallow.List, narrowOnly bool, e audit.Entry) error
```

**Token routes** (two, as a revoke has two):

| Route | Action and object | Scope | Answers |
| --- | --- | --- | --- |
| `PUT` and `DELETE` `/api/dustpan/v1/me/tokens/{id}/ip-allowlist` | `token:manage-own` on the caller (`pkg/identity/identity.go:385`, `resolveCaller`) | `ScopeOwn` | 200 with the token resource on `PUT`, 204 on `DELETE`. 404 for a token of another person or an unknown token. |
| `PUT` and `DELETE` `/api/server/service-tokens/{id}/ip-allowlist` | `principal:manage-tokens` on the service principal of the token (`resolveTokenOwner`, `internal/api/server/tokens.go:436`) | `ScopeService` | 200 on `PUT`, 204 on `DELETE`. The same 403 for a missing token, a personal token and a refused caller (`OpaqueDenial`, as the rotate route). |

Request body of `PUT`: `{"cidrs":["203.0.113.0/24"]}`. Each route is wrapped like the other token changes with `serveTokenChange` (`internal/api/server/tokens.go:267`), and has no `freshFirst`. Errors: 400 `ip-allowlist.invalid-cidr` for a bad list (`internal/errcode/catalog_ipallowlist.go:8`), 400 for more than 64 entries or 4096 bytes, 403 `ip-allowlist.widen-refused` (NEW code in the same catalog file) for `ErrListWidening`. The token resource shows `ipAllowlist` as a list of CIDR text, `[]` when none (the effective value, "No magic").

**Audit actions**: a denial writes `installation:deny-ip-allowlist` with the scope `token` (`pkg/dustpan/ipallow.go:418`). The entry carries the client address and the token ID. It never names the token in the response. A change of a token list writes `token:set-ip-allowlist` or `token:remove-ip-allowlist`, with the caller, the token ID and, when the caller used a token, that token ID. A refused widening writes a failure entry with the same fields (`appendServerChangeReason`, as `serveTokenChange` does for other refusals).

## Structure

The checker is the unexported `allowChecker` (`pkg/dustpan/ipallow.go:62,74`). This change gives it a token slot in place of the namespace slot. The token field of `accesstoken.Token` is unexported, and its constructor takes an `ipallow.List`.

## Risks

- A token list that excludes the address of the CI runner blocks that pipeline. The administrator clears the list from a console session, or revokes and recreates the token. A request made with the token itself cannot clear the list, because a clear is a widening.
- A token may widen the list of another token that its maker or manager could change. Owner decision 8 names the same token only. A stolen token cannot use this to reach a token whose secret the thief lacks.
- A stolen console session of a maker may widen a list with no fresh step. A revoke needs none either (owner decision 8).
- A wrong client address behind a proxy blocks valid tokens.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The token store refuses a value that does not decode, and the read denies. | Store `not-a-cidr` straight in the column and check that the request gets 403. |
| service | `SetIPAllowlist` refuses an empty list, 65 entries, a bad entry and a value over 4096 bytes. | Call the service with each and check `ErrInvalidList` or the limit error. |
| service | The checker denies when any opted-in list denies. | Use an address in the token list but outside the installation list and check denial with scope `installation`. Use an address in the installation list but outside the token list and check scope `token`. |
| HTTP handler | The handler answers 403 for the token list after authentication and writes no other entry. | Send a valid token from an address outside its list and check 403, the audit entry and the token last-used time. |
| service | The guard: a caller whose token ID is the target token may only narrow. | Call with `caller.TokenID == id` and a wider list, a clear, a disjoint list and an equal list. The first three give `ErrListWidening`; the equal list and a narrower one pass. Mutant: remove the comparison and the table fails. |
| service | The scope check: the maker for a personal token, the holder for a service token. | Call as another person, as a `server:manage-tokens` holder on a personal token, as a holder on another principal, as a person with no grant. Each is refused and stores no change. |
| port adapter | The store guard `narrowOnly` compares inside the write transaction. | Call the store direct with `narrowOnly` true and a wider list, with the service check skipped. The store returns `ErrListWidening` and the stored list stays. Mutant: drop the flag check in one adapter and the conformance case fails. |
| HTTP handler | The route needs its action. A token that widens its own list gets 403. | Call each route with no action, with a token as the caller and a wider list, and for a token of another person. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| latency | The token list adds no query to a request: it comes in the digest lookup. | queries added per request | each request with an access token |

## Threat model

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| network to server | E | A stolen token works from any address. | authorize at point of use | The token list is checked on each request after authentication. | Use a valid token from an address outside its list and check 403 scope `token`. |
| network to server | E | A token holder widens or clears its own list. | limit the effect | The guard in the service and in the store: a request made with the token only narrows. | Call each route with the token itself and a wider list, a clear and a disjoint list. Check 403 `ip-allowlist.widen-refused` and an unchanged list. |
| network to server | E | A person changes the list of a token that it does not manage. | authorize at point of use | The scope check of a revoke. | Call each route as another person, as a `server:manage-tokens` holder on a personal token and as a holder on another principal. Check 404 or 403 and an unchanged list. |
| network to server | R | A change of a list leaves no record. | fail closed | The audit entry commits with the change; a refusal writes a failure entry. | Make the audit write fail and check that the list does not change. |
| network to server | I | The 403 names a token and so leaks that it exists. | encode or contain | The scope is a fixed value, never text of the request. | Deny a token with a known ID and check that the body holds no ID. |
| store to server | T | A restored backup holds a token list that does not decode. | fail closed | `DecodeList` refuses the value, and the request is denied. | Restore a bad value and check 403. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| request | The token list does not decode. | Clear the list of the token, or repair the row. | The server answers 403 for that token and writes a denial entry. |
| list change | The audit entry cannot be written. | Retry the change. | The change does not happen. |
