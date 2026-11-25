## Decisions

- The proof is read where the session is known (the handler) and checked where the rule lives (the service and the store). No layer trusts the one before it.
- One function maps claims to the proof, so two handlers cannot map them in two ways.

## Contract

```go
// pkg/session/proof.go (NEW)

// FreshProof is the fresh authentication that a session carries for the job
// service. The zero FreshProof is no proof.
type FreshProof struct {
	Kind FreshProofKind
	At   time.Time
}

type FreshProofKind string

const (
	FreshProofNone FreshProofKind = ""
	FreshProofMFA  FreshProofKind = "mfa"
	FreshProofSSO  FreshProofKind = "sso"
)

// FreshProofOf maps the claims of an opened access cookie. passkey and mfa give
// FreshProofMFA, sso gives FreshProofSSO, every other kind and a zero FreshAt give the
// zero FreshProof. It never reads the clock: the service compares the age.
func FreshProofOf(c Claims) FreshProof
```

`sessionauth.Caller` gets the field `FreshProof session.FreshProof`, set by `Checker.Authenticate` from the opened cookie. The jobs handlers copy it to `job.Actor.ConfirmAuth` and `job.Actor.AuthAt` only for a request that the session cookie signed and for a person. `jobs.Handler` takes no new dependency.

## Layers

| Layer | Check | Hostile case |
|---|---|---|
| `session.FreshProofOf` | allowlist of kinds, zero time gives no proof | unknown kind, `password`, future `FreshAt` |
| `sessionauth.Checker` | proof only from a sealed and opened cookie | forged cookie, body field `authAt`, header |
| Handler | no proof for a token, an agent or a non-person | access token with a cookie-like header |
| Service | age, kind and subject (existing) | stale, `sso` for a destroy |
| Store | age on the store clock (existing) | clock step, proof after the approval time |

## Risks

- A cookie older than the window gives a stale proof. The service refuses it. The refusal names the sign-in path.
- The handler may pass a proof with the setting off. The policy of `fresh-auth-enabled-setting` ignores it. A test shows both.
