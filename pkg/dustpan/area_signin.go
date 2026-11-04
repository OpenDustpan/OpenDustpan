package dustpan

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
)

// signInGateSettings holds the failure gates that gatedAuthenticator builds
// for the token lookups, the sign-in routes and the password routes.
type signInGateSettings struct {
	// gate is the per-address failure limit of the token lookups.
	// wireTokens sets it; nil means no limit.
	gate *authgate.Gate
	// signInGate is the per-address limit of the sign-in routes. It has its
	// own budget, so sign-ins never slow a token lookup. wireTokens sets it
	// with gate; nil means no limit. Each gate keeps its own LRU of sources,
	// so the gate memory of one process is at most 2 x 10,000 buckets.
	signInGate *authgate.Gate
	// passwordGate is the per-address limit of the routes that check a
	// password or a one-time code of a local user. It has its own budget, so
	// a guess of a password never slows another sign-in. wireTokens sets it
	// with gate; nil mounts no such route.
	passwordGate *authgate.Gate
}

// defaultSignInGateSettings returns the gate settings before
// gatedAuthenticator runs: no gate.
func defaultSignInGateSettings() signInGateSettings {
	return signInGateSettings{gate: nil, signInGate: nil, passwordGate: nil}
}

// gatedAuthenticator returns the authenticator that every adapter and the
// request log use: the configured authenticator first, so the bootstrap
// admin tokens (ADR 6) never meet the failure budget, then the stored access
// tokens when the store keeps them, behind a gate. The gate resolves a
// token once for each request and slows the misses of secret-shaped values
// from one source. It never refuses a valid token.
//
//nolint:ireturn // The adapters take the port.
func gatedAuthenticator(cfg *settings, tokens *accesstoken.Service) (identity.Authenticator, error) {
	if tokens == nil {
		return cfg.authenticator, nil
	}

	// failures shares the failure budget of every instance on one database, or
	// keeps the budget in the process when the store keeps no counter.
	var failures authgate.FailureCounter
	if counter, ok := cfg.namespaceStore.(authgate.FailureCounter); ok {
		failures = counter
	}

	gate, err := authgate.New(authgate.Config{
		Next: tokens, Charge: accesstoken.LooksLikeSecret, Logger: cfg.logger, Now: cfg.now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: "",
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("authentication gate: %w", err)
	}

	cfg.signInGates.gate = gate

	signInGate, err := authgate.New(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: cfg.logger, Now: cfg.now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: signInGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("sign-in gate: %w", err)
	}

	cfg.signInGates.signInGate = signInGate

	passwordGate, err := authgate.New(authgate.Config{
		Next: refuseAll{}, Charge: nil, Logger: cfg.logger, Now: cfg.now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: passwordGateScope,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("password gate: %w", err)
	}

	cfg.signInGates.passwordGate = passwordGate

	return identity.Chain{cfg.authenticator, gate}, nil
}

// signInGateScope names the budget of the sign-in routes in the shared
// count.
const signInGateScope = "signin"

// passwordGateScope names the budget of the routes that check a password or
// a one-time code of a local user in the shared count.
const passwordGateScope = "password"

// refuseAll is the Next of the sign-in gate. The sign-in routes never look
// up a token through that gate; if one does, it gets no principal.
type refuseAll struct{}

func (refuseAll) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	var none identity.Principal

	return none, identity.ErrUnauthenticated
}
