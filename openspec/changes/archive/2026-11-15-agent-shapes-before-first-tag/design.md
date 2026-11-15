## Context

ADR 354 (owner) adds the kind `agent` and makes an unknown kind fail closed. The change is the smallest step: every reader of the kind knows `agent` or refuses it, before any agent record exists.

## Decisions

1. **One more value, the same parse.** `KindAgent` joins `principal.Kind` and `identity.Kind`; `principal.ParseKind` and `identity.Kinds` list it. An unknown value stays `ErrInvalid` (`principal.invalid`).
2. **Every switch knows `agent` or refuses it.** The switches at internal/subscriptionsvc/subscriptionsvc.go:382, internal/api/registry/handlers.go:163, internal/api/inventory/views.go:231, pkg/notify/sinks/sinks.go:691 and pkg/accesstoken/accesstoken.go:82 each get an explicit `agent` case or keep a default that refuses; none treats `agent` as a person.
3. **The audit entry accepts `agent`.** `audit.Entry.validatePrincipal` (pkg/audit/audit.go:327) accepts the principal kind `agent`, so an entry of an agent is not refused; the hash encoding of `principalKind` does not change.
4. **No agent record from the create route.** The create route of principals refuses `agent`: an agent needs its sponsors (RFC 121 decision 4) and comes from the model connect request (decision 7).

## Structure

No new type: one constant in each kind type, and the existing parse functions.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | The create route accepts person, service and workload only. | `TestCreatePrincipalRefusesAgentAndUnknownKinds` (NEW, internal/api/server): `agent`, `robot`, `Agent`, `""`, `agent\n` each answer 400 `principal.invalid`, and no row is stored. |
| service | `principal.ParseKind` refuses an unknown kind. | `TestParseKindTable` (NEW, pkg/principal): the four kinds pass; `robot`, `Agent`, ` agent`, `agent\x00` are `ErrInvalid`. |
| each switch | An explicit `agent` case or a refusing default. | `TestKindSwitchesKnowAgent` (NEW) in each package of decision 2: `agent` gives the chosen result, and an unknown kind gives the refusal. |
| audit | `validatePrincipal` refuses an unknown kind and accepts `agent`. | `TestValidatePrincipalKinds` (NEW, pkg/audit): `agent` passes; `robot` is `ErrInvalidEntry`. |

## Contract

```go
// pkg/principal
const KindAgent Kind = "agent" // NEW
// pkg/identity
const KindAgent Kind = "agent" // NEW; Kinds() returns it too
```

No new route, table, column or error code. `POST /api/server/principals` answers 400 `principal.invalid` for the kind `agent` (as for any kind that it does not make).
