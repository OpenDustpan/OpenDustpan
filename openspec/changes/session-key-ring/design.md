## Decisions

- One ring for every cookie kind. The associated data names the kind, so a value of one kind never opens as another.
- The ring is read from SQL at the start, each minute and on an unknown key ID (at most each 10 seconds).

## Contract

```go
// pkg/session/keyring.go (NEW)
type Ring struct{ /* unexported */ }

func NewRing(store KeyStore, wrapper secret.Wrapper, clock func() time.Time, audit AuditTemplate) (*Ring, error)
func (r *Ring) Sealer() Sealer           // the seal of the access and the refresh cookie
func (r *Ring) StateSealer() StateSealer // the state of a sign-in and the nonces
func (r *Ring) Rotate(ctx context.Context, retireAll bool, e audit.Entry) error
```

Fields are unexported. `NewRing` checks that each stored version unwraps and refuses to start otherwise.

## Layers

| Layer | Check | Hostile case |
|---|---|---|
| Sealer open | kind, version, key ID, length, strict JSON | value of another kind, 4097 bytes, unknown key ID, changed byte |
| Ring | seal only after `SealsFrom`, retired never opens | version sealing early, retired version |
| Store | the append writes its audit entry in one transaction | entry that fails validation |
| Route | read needs `installation:manage-sign-in`, shows no key | token without the action |

## Risks

- A nonce collision after 2^32 seals of one version. The seal count of ADR 297 decision 9 adds a version at 2^27 on a replica.
