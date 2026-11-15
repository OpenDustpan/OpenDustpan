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
	"opendustpan.org/opendustpan/pkg/namespace"
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

// gatedAuthenticator returns the authenticator that every adapter and the
// request log use, and the gates that it builds: next first, so the
// bootstrap admin tokens never meet the failure budget, then the stored
// access tokens when the store keeps them, behind a gate. The gate resolves a
// token once for each request and slows the misses of secret-shaped values
// from one source. It never refuses a valid token. With nil tokens it returns
// next and no gate.
//
//nolint:ireturn // The adapters take the port.
func gatedAuthenticator(
	next identity.Authenticator, tokens *accesstoken.Service, store namespace.Store,
	logger *slog.Logger, now func() time.Time,
) (identity.Authenticator, signInGates, error) {
	none := noSignInGates()

	if tokens == nil {
		return next, none, nil
	}

	// failures shares the failure budget of every instance on one database,
	// or keeps the budget in the process when the store keeps no counter.
	var failures authgate.FailureCounter
	if counter, ok := store.(authgate.FailureCounter); ok {
		failures = counter
	}

	gate, err := authgate.New(authgate.Config{
		Next: tokens, Charge: accesstoken.LooksLikeSecret, Logger: logger, Now: now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: "",
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, none, fmt.Errorf("authentication gate: %w", err)
	}

	signInGate, err := authgate.New(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: logger, Now: now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: signInGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, none, fmt.Errorf("sign-in gate: %w", err)
	}

	passwordGate, err := authgate.New(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: logger, Now: now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: passwordGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, none, fmt.Errorf("password gate: %w", err)
	}

	passkeyGate, err := newPasskeyFailureGate(failures, logger, now)
	if err != nil {
		return nil, none, fmt.Errorf("passkey gate: %w", err)
	}

	passkeyBeginGate, err := authgate.NewRefusing(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: logger, Now: now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: passkeyBeginGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, none, fmt.Errorf("passkey begin gate: %w", err)
	}

	gates := signInGates{
		gate: gate, signInGate: signInGate, passwordGate: passwordGate, passkeyGate: passkeyGate,
		passkeyBeginGate: passkeyBeginGate,
	}

	return identity.Chain{next, gate}, gates, nil
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
