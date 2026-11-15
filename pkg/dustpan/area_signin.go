package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/removalalertsvc"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// signInGates holds the failure gates that gatedAuthenticator builds for the
// token lookups, the sign-in routes and the password routes. NewServer passes
// them to the console sign-in. They are not settings: no option sets them.
type signInGates struct {
	// gate is the per-address failure limit of the token lookups. nil means
	// no limit.
	gate *authgate.Gate
	// signInGate is the per-address limit of the sign-in routes. It has its
	// own budget, so sign-ins never slow a token lookup. gatedAuthenticator
	// sets it with gate; nil means no limit. Each gate keeps its own LRU of
	// sources, so the gate memory of one process is at most 2 x 10,000
	// buckets.
	signInGate *authgate.Gate
	// passwordGate is the per-address limit of the routes that check a
	// password or a one-time code of a local user. It has its own budget, so
	// a guess of a password never slows another sign-in. gatedAuthenticator
	// sets it with gate; nil mounts no such route.
	passwordGate *authgate.Gate
	// passkeyGate is the per-address limit of the failed responses of a
	// passkey sign-in, and passkeyBeginGate is the per-address limit of the
	// begins of a passkey sign-in. Each has its own budget, so a passkey
	// ceremony never slows another sign-in. gatedAuthenticator sets them with
	// gate; nil mounts no passkey sign-in route.
	passkeyGate      *authgate.Gate
	passkeyBeginGate *authgate.Gate
}

// noSignInGates returns the gates of a store that keeps no access tokens:
// no gate.
func noSignInGates() signInGates {
	return signInGates{gate: nil, signInGate: nil, passwordGate: nil, passkeyGate: nil, passkeyBeginGate: nil}
}

// gateEnv holds what every failure gate of the token lookups shares: the count
// of the failures of the instances on one database, the logger and the clock.
// failures is nil when the store keeps no shared count.
type gateEnv struct {
	failures authgate.FailureCounter
	logger   *slog.Logger
	now      func() time.Time
}

// newGateEnv returns the environment of the gates of cfg. A store that keeps a
// shared failure count gives it, so every instance on one database shares the
// budget.
func newGateEnv(cfg *settings) gateEnv {
	env := gateEnv{failures: nil, logger: cfg.logger, now: cfg.now}

	if counter, ok := cfg.namespaceStore.(authgate.FailureCounter); ok {
		env.failures = counter
	}

	return env
}

// gatedAuthenticator returns the authenticator that every adapter and the
// request log use, and the gates that it builds: next first, so the
// bootstrap admin tokens never meet the failure budget, then the stored
// access tokens when the store keeps them, behind a gate, and the job tokens
// of the runner sessions when the server issues them, behind a gate of their
// own. It never refuses a valid token. With nil tokens and nil jobs it
// returns next and no gate.
//
// Each kind of token goes to the gate of its own kind only, see tokenKinds.
// The gate of the job tokens has its own budget scope, so a guess of a job
// token never spends the budget of the access tokens and the reverse. Its
// budget also lives in the process when the store keeps no shared counter.
//
//nolint:ireturn // The adapters take the port.
func gatedAuthenticator(
	next identity.Authenticator, tokens *accesstoken.Service, jobs *jobtoken.Service, env gateEnv,
) (identity.Authenticator, signInGates, error) {
	if tokens == nil && jobs == nil {
		return next, noSignInGates(), nil
	}

	accessGate, gates, err := newAccessTokenGates(tokens, env)
	if err != nil {
		return nil, noSignInGates(), err
	}

	// The gate of the job tokens exists only when the server issues them.
	var jobGate *authgate.Gate

	if jobs != nil {
		jobGate, err = newJobTokenGate(jobs, env)
		if err != nil {
			return nil, noSignInGates(), fmt.Errorf("job token gate: %w", err)
		}
	}

	return chainOf(next, accessGate, jobGate), gates, nil
}

// newAccessTokenGates returns the failure gate of the access token lookups and
// the gates of the sign-in routes. A nil tokens serves no access token: then
// no gate and no route of a sign-in.
func newAccessTokenGates(tokens *accesstoken.Service, env gateEnv) (*authgate.Gate, signInGates, error) {
	if tokens == nil {
		return nil, noSignInGates(), nil
	}

	gate, err := authgate.New(authgate.Config{
		Next: tokens, Charge: accesstoken.LooksLikeSecret, Logger: env.logger, Now: env.now,
		Failures: env.failures, Window: 0, CounterErrors: nil, Scope: "",
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, noSignInGates(), fmt.Errorf("authentication gate: %w", err)
	}

	signInGate, err := authgate.New(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: env.logger, Now: env.now,
		Failures: env.failures, Window: 0, CounterErrors: nil, Scope: signInGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, noSignInGates(), fmt.Errorf("sign-in gate: %w", err)
	}

	passwordGate, err := authgate.New(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: env.logger, Now: env.now,
		Failures: env.failures, Window: 0, CounterErrors: nil, Scope: passwordGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, noSignInGates(), fmt.Errorf("password gate: %w", err)
	}

	passkeyGate, err := newPasskeyFailureGate(env.failures, env.logger, env.now)
	if err != nil {
		return nil, noSignInGates(), fmt.Errorf("passkey gate: %w", err)
	}

	passkeyBeginGate, err := authgate.NewRefusing(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: env.logger, Now: env.now,
		Failures: env.failures, Window: 0, CounterErrors: nil, Scope: passkeyBeginGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, noSignInGates(), fmt.Errorf("passkey begin gate: %w", err)
	}

	gates := signInGates{
		gate: gate, signInGate: signInGate, passwordGate: passwordGate, passkeyGate: passkeyGate,
		passkeyBeginGate: passkeyBeginGate,
	}

	return gate, gates, nil
}

// chainOf returns the chain of the authenticators of the server: next first,
// so the bootstrap admin tokens never meet a failure budget, then the member
// that resolves a token, with one gate for each kind that the server serves.
func chainOf(next identity.Authenticator, accessGate, jobGate *authgate.Gate) identity.Chain {
	chain := identity.Chain{next}

	if accessGate != nil && jobGate != nil {
		return append(chain, tokenKinds{access: accessGate, job: jobGate})
	}

	if accessGate != nil {
		return append(chain, accessGate)
	}

	if jobGate != nil {
		return append(chain, jobGate)
	}

	return chain
}

// tokenKinds resolves a token with the gate of its kind: a value with the form
// of a job token with the gate of the job tokens, every other value with the
// gate of the access tokens.
//
// One value goes to one gate only. A request keeps the answer that it resolved
// before, keyed by the digest of the token, so a gate that saw a value of the
// other kind would keep a failure for it and would hide that value from the
// gate of its own kind.
type tokenKinds struct {
	access identity.Authenticator
	job    identity.Authenticator
}

var _ identity.Authenticator = tokenKinds{access: nil, job: nil}

// AuthenticateToken implements identity.Authenticator.
func (k tokenKinds) AuthenticateToken(ctx context.Context, raw string) (identity.Principal, error) {
	if jobtoken.LooksLikeSecret(raw) {
		return k.job.AuthenticateToken(ctx, raw)
	}

	return k.access.AuthenticateToken(ctx, raw)
}

// jobTokenGateScope names the budget of the job token lookups in the shared
// count. It is apart from the budget of the access tokens, so a flood of
// guesses of one kind never slows the sign-in of the other.
const jobTokenGateScope = "jobtoken"

// errNoJobTokens reports a gate of job tokens with no service to resolve
// them. A gate without a service would drop every job token, so the build
// fails instead.
var errNoJobTokens = errors.New("dustpan: the job token gate needs the job token service")

// newJobTokenGate returns the failure gate of the job token lookups. It
// charges only a value with the form of a job token, and its budget has its
// own scope, so a guess of a job token never spends the budget of the access
// tokens.
func newJobTokenGate(jobs *jobtoken.Service, env gateEnv) (*authgate.Gate, error) {
	// Defense in depth: gatedAuthenticator builds this gate only when the
	// server issues job tokens. This check stays so that a new caller cannot
	// build a gate over a nil service, which would authenticate nothing and
	// would take the failure budget of the address with it.
	if jobs == nil {
		return nil, errNoJobTokens
	}

	gate, err := authgate.New(authgate.Config{
		Next: jobs, Charge: jobtoken.LooksLikeSecret, Logger: env.logger, Now: env.now,
		Failures: env.failures, Window: 0, CounterErrors: nil, Scope: jobTokenGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("job token gate: %w", err)
	}

	return gate, nil
}

// signInGateScope names the budget of the sign-in routes in the shared
// count.
const signInGateScope = "signin"

// passwordGateScope names the budget of the routes that check a password or
// a one-time code of a local user in the shared count.
const passwordGateScope = "password"

// passkeyGateScope names the budget of the failed responses of a passkey
// sign-in in the shared count.
const passkeyGateScope = "passkey"

// The failure budget of a passkey sign-in: 20 failed responses of one source
// address refuse that address for 15 minutes. The window of the shared count
// is the same 15 minutes. A passkey cannot be guessed, so this limit bounds
// the load of the route and not the guesses of a secret.
const (
	passkeyFailureLimit  = 20
	passkeyFailureWindow = 15 * time.Minute
)

// newPasskeyFailureGate returns the gate of the failed responses of a passkey
// sign-in. It refuses a source over the budget at once, with no wait. The
// budget refills one unit in the window, so a source that spent it stays
// refused for the window.
func newPasskeyFailureGate(
	failures authgate.FailureCounter, logger *slog.Logger, now func() time.Time,
) (*authgate.Gate, error) {
	gate, err := authgate.NewRefusing(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: logger, Now: now,
		Failures: failures, Window: passkeyFailureWindow, CounterErrors: nil, Scope: passkeyGateScope,
		Burst: passkeyFailureLimit, Refill: passkeyFailureWindow, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("passkey failure gate: %w", err)
	}

	return gate, nil
}

// passkeyBeginGateScope names the budget of the begins of a passkey sign-in in
// the shared count.
const passkeyBeginGateScope = "passkeybegin"

// refuseAll is the Next of the sign-in gate. The sign-in routes never look
// up a token through that gate; if one does, it gets no principal.
type refuseAll struct{}

func (refuseAll) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	var none identity.Principal

	return none, identity.ErrUnauthenticated
}

// redirectMethod returns the sign-in method of kind over client for the
// provider id. enabled reports whether the provider is on: the method reads
// it at each start and each callback, and fails closed when it cannot.
func redirectMethod(
	client oauth.MethodClient, id string, kind session.MethodKind,
	enabled func(ctx context.Context) (bool, error), now func() time.Time,
) (*oauth.Method, error) {
	m, err := oauth.NewMethod(oauth.MethodConfig{
		Client: client, Enabled: enabled, Now: now, ProviderID: id, Kind: kind,
	})
	if err != nil {
		return nil, fmt.Errorf("sign-in method of provider %s: %w", id, err)
	}

	return m, nil
}

// configuredProviderOn is the switch of a provider of the configuration of
// the server. No API turns such a provider off: a change of the
// configuration and a restart remove it.
func configuredProviderOn(context.Context) (bool, error) {
	return true, nil
}

// signInServiceInputs holds the values that newSignInService reads.
type signInServiceInputs struct {
	principals *principal.Service
	// store is the namespace store. It must keep the principal records and the
	// session generations of the principals, else the sign-in service cannot
	// disable a person that an identity provider reports as disabled.
	store    any
	sessions *session.Sessions
	audit    audit.ServerStore
	logger   *slog.Logger
	// onAuditFailure is called when the audit entry of a refused sign-in is
	// not written. nil means no call.
	onAuditFailure func()
	// removals records the removals of a group claim sync for the removal
	// alert. nil records nothing.
	removals *removalalertsvc.Service
}

// errSignInStore reports a store that cannot serve the sign-in service.
var errSignInStore = errors.New("the store cannot serve the sign-in service")

// newSignInService builds the sign-in service of the console sign-in. It has
// no group applier on purpose. The stores implement the applier over explicit
// group mappings (GroupClaimApplier of the store), but the rules for protected
// groups have no stored data yet: no closed or trusted flag on a group, no
// full-sync setting and no protection check. An applier without those rules
// could add a member to a group with wide reach from an identity provider.
// The applier is not wired until the protection rules exist, so no sign-in
// joins a group.
func newSignInService(in signInServiceInputs) (*signinsvc.Service, error) {
	records, ok := in.store.(principal.RecordStore)
	if !ok {
		return nil, fmt.Errorf("%w: it keeps no principal records", errSignInStore)
	}

	generations, ok := in.store.(session.PrincipalStateStore)
	if !ok {
		return nil, fmt.Errorf("%w: it keeps no session generations", errSignInStore)
	}

	var removals signinsvc.Removals
	if in.removals != nil {
		removals = in.removals
	}

	tempAdmins, ok := in.store.(principal.TempAdmins)
	if !ok {
		return nil, fmt.Errorf("%w: it keeps no temporary administrators", errSignInStore)
	}

	// A store without the counts or their key leaves both nil: the sign-in of
	// a password method then refuses every request.
	var (
		locks    authgate.NameLockCounter
		nameKeys signinsvc.NameKeys
	)

	if counter, hasCounter := in.store.(authgate.NameLockCounter); hasCounter {
		locks = counter
	}

	if keys, hasKeys := in.store.(signinsvc.NameKeys); hasKeys {
		nameKeys = keys
	}

	svc, err := signinsvc.New(signinsvc.Config{
		Principals:     in.principals,
		Disabler:       records,
		Generations:    generations,
		Sessions:       in.sessions,
		Audit:          in.audit,
		TempAdmins:     tempAdmins,
		Groups:         nil,
		Removals:       removals,
		States:         generations,
		Locks:          locks,
		NameKeys:       nameKeys,
		Logger:         in.logger,
		OnAuditFailure: in.onAuditFailure,
	})
	if err != nil {
		return nil, fmt.Errorf("sign-in service: %w", err)
	}

	return svc, nil
}
