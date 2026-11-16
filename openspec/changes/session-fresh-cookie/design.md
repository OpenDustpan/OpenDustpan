## Decisions

- The stored session answers who. The cookie answers how fresh. Neither can answer the other question.
- One function builds the claims from the sign-in result, so six handlers cannot build them in six ways.

## Contract

```go
// pkg/session/fresh.go (NEW)

// FreshFact is the fresh authentication that a request carries. The zero
// FreshFact is no fact.
type FreshFact struct {
	Kind FreshKind
	MFAAt time.Time
	At   time.Time
}

// ClaimsOf builds the claims of an access cookie from a started session and
// the way the person authenticated. It sets a kind and a time only for a
// step that happened in this sign-in.
func ClaimsOf(s Session, how SignInMethod, now time.Time) Claims

// BindFact returns the fact of c when c belongs to the stored session m, and
// the zero FreshFact otherwise.
func BindFact(c AccessClaims, m Match, now time.Time) FreshFact
```

`sessionauth.Caller` gets the field `Fresh session.FreshFact`. The handler keeps the sealed value in memory only for the response.

## Layers

| Layer | Check | Hostile case |
|---|---|---|
| `ClaimsOf` | allowlist of methods, no time for a method that gives no step | dev sign-in, password alone |
| `BindFact` | principal, generation, sign-in time, expiry | cookie of another person, of an older generation, of another sign-in, expired |
| `sessionauth` | cookie opens with the ring, stored session first | forged value, value of another kind, cookie without a stored session |
| Handler | no cookie for a token, an agent or a failed sign-in | failed sign-in, agent |
| Re-issue | copies claims, never advances a time | request that tries to move `freshAt` |

## Risks

- Two credentials on the console until the later change moves the authority. The cookie alone authenticates nothing: the stored session must match.
