## Context

ADR 385 makes the fresh step opt-in with one installation setting, `freshAuthEnabled`. Today about 30 call sites ask for a step on their own rule (see "Evidence" in the proposal). A new call site that forgets the setting is the bug class. The fix must make that class impossible, not add a new check at each site.

## Decisions

1. **One decision point.** A NEW package `pkg/freshstep` holds the NEW type `Policy`. Each call site asks the NEW `Policy.Required(ctx)` before it asks for a step. With the setting off, `Required` answers false and the site skips its check. With the setting on, or when the setting cannot be read, it answers true (fail closed).
2. **The step check is reachable only through the policy.** The existing freshness ports (`accesstoken.Freshness.RequireFresh`, the freshness handlers of the four tool adapters (internal/api/tfe/freshness.go, internal/api/pulumi/freshness.go, internal/api/profile/freshness.go, internal/api/issuer/freshness.go), `profile.requireFresh`, `setting` weaker-change check, the job destroy check) become methods of the NEW unexported-field type `freshstep.Guard`, made only by the NEW `freshstep.NewGuard(policy, check)`. The NEW `Guard.Require` returns nil when `Required` is false, else it runs `check`. A site holds a `Guard`, never the bare `check`. A new site that takes a bare check does not compile against the constructors of the services.
3. **Same value in each layer.** The handler, the service and the store each hold the same `Policy` value, made once in the server wiring. None keeps its own rule (ADR 385 decision 2).
4. **Setting declaration.** `freshAuthEnabled` is a boolean declaration of the installation scope in the settings registry (`pkg/setting`), effective default false, with the mark "reduces security when it goes from on to off". The static layer (flag, environment variable, configuration file) may set it, and that needs no step. `pkg/setting/change.go` treats the change from true to false as a weaker change (it asks for a step while the setting is on), and the change from false to true as no weaker change.
5. **Stored rules show they are not enforced.** The rule API and the namespace settings API add the boolean field `enforced` next to `freshAuthSeconds` and `freshAuthForApprovals`. It is `false` when the policy says the step is not required. The server never rewrites a stored rule.
6. **Out of the policy.** The first-administrator code, the factor change and reset, the enrollment code and the break-glass proof keep their own rules (ADR 385 decision 5). They do not take a `Guard`. A test lists them, so that a later owner decision moves one deliberately.
7. **No hidden rule.** The turn on or off writes an audit entry with the old and the new value (decision 4 of ADR 385). A step skipped because the setting is off writes no entry of its own, because nothing was asked. The approval entry of a `destroy` records `auth: none` in that case.

## Structure

The NEW type `freshstep.Guard` has unexported fields. Only `NewGuard` makes one. A call site gets a `Guard` by injection, so the setting cannot be skipped or forgotten. No lint rule and no new tool is added. A task checks with `rg` that no file outside `pkg/freshstep` and the wiring calls a bare freshness check.

## Contract

```go
// Package freshstep decides whether a fresh authentication is required.
package freshstep

// Policy reports whether the installation requires a fresh step.
type Policy interface {
	// Required returns true when the setting freshAuthEnabled is on, or when
	// the setting cannot be read (fail closed). It never returns an error.
	Required(ctx context.Context) bool
}

// Guard asks for a fresh step only when the policy requires one.
type Guard struct{ /* unexported */ }

// NewGuard returns a Guard. A nil policy or a nil check is an error.
func NewGuard(p Policy, check func(ctx context.Context, subject string) error) (Guard, error)

// Require returns nil when the policy does not require a step, else the
// result of the check (ErrFreshStepRequired of the domain package).
func (g Guard) Require(ctx context.Context, subject string) error
```

Settings API: `GET /api/dustpan/v1/settings` lists `freshAuthEnabled` with `value`, `default` and `source` as for each setting (ADR 212 decision 9). `PATCH /api/dustpan/v1/settings` changes it, with the authorization action of the settings write. The rule and namespace APIs add `enforced` (boolean) to each of `freshAuthSeconds` and `freshAuthForApprovals`. Error: 403 `mfa.fresh-step-required` as before.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Port adapter (store) | `checkDestroyProof` reads the same `Policy`. With the setting on, an approval of a `destroy` with no proof is refused. | A store call with no proof and the setting on is refused, and the guard removed makes the test fail. |
| Service | `pkg/job`, `pkg/accesstoken`, `pkg/profile`, `pkg/setting` hold a `Guard`. | A service call with an access token subject and the setting on is refused. |
| HTTP handler | Each adapter handler holds the same `Guard`. | A request with an access token and the setting on gets 403 `mfa.fresh-step-required`. |
| Setting read | `Required` fails closed. | A setting store that returns an error gives `Required` true. |

Each guard keeps a comment that says it is a defense-in-depth layer and names the layers that it does not rely on.

## Risks

- The call-site list of the proposal is a snapshot. Task 1.1 runs `rg` again on the current main and gates each site it finds.
- Code that lands before this change (#3283, #3286) adds sites. Their lanes add them to the list when this change merges.
- With the setting off, the grant and the audit entry are the only checks of a `destroy` approval (ADR 385 consequences). The guide says it.
