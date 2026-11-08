package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

const budgetAddress = "192.0.2.7"

// budgetFixture is the gates of gatedAuthenticator over a real token store,
// and one valid token.
type budgetFixture struct {
	cfg    *settings
	tokens *accesstoken.Service
	chain  identity.Authenticator
	gates  signInGates
	valid  string
}

func newBudgetFixture(t *testing.T) budgetFixture {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil, Store: st, Principals: st, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil,
		Now:    func() time.Time { return now }, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil, Store: st, Guard: principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil,
		GroupReach: nil, Now: func() time.Time { return now }, NewID: nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "admin",
		Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
	entry := audit.Entry{
		At: now, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "",
		Action: string(identity.ActionServerManageTokens), Object: "", Version: "", ValuePath: "",
		ValueCount: nil, SourceIP: budgetAddress, UserAgent: "test", Channel: audit.ChannelAPI, Kind: "",
		Outcome: "", RequestID: "", PrevHash: "", Hash: "", Sequence: 0, Changes: nil, ObjectID: "",
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	owner, err := principals.CreatePrincipal(t.Context(), entry,
		principal.Input{Subject: "ci-budget", DisplayName: "", Kind: principal.KindService})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	_, secret, err := tokens.Create(t.Context(), admin, entry, accesstoken.ScopeService, accesstoken.Input{
		ExpiresAt: now.Add(24 * time.Hour), PrincipalID: owner.ID, Name: "budget",
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	cfg := defaultSettings()
	cfg.authenticator, cfg.namespaceStore = refuseAll{}, st
	cfg.logger, cfg.now = slog.New(slog.DiscardHandler), func() time.Time { return now }

	chain, gates, err := gatedAuthenticator(cfg.authenticator, tokens, cfg.namespaceStore, cfg.logger, cfg.now)
	if err != nil {
		t.Fatalf("gated authenticator: %v", err)
	}

	return budgetFixture{cfg: cfg, tokens: tokens, chain: chain, gates: gates, valid: secret.Reveal()}
}

// guess is a value with the form of a secret that no token holds.
func guess() string {
	return accesstoken.SecretPrefix + strings.Repeat("A", 43)
}

// canceledContext returns a context of a request from budgetAddress that is
// already done: a request over its budget then ends its wait at once with
// identity.ErrRateLimited, and a request within it goes through.
func canceledContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	return authgate.WithRequest(ctx, budgetAddress)
}

// budgetCase is one case of TestSignInBudgetIsApartFromTokenLookups.
type budgetCase struct {
	name string
	// signIns is the count of sign-in starts from the address, and guesses
	// the count of failed token lookups.
	signIns, guesses int
	// tokenLimited and signInLimited say which budget is spent.
	tokenLimited, signInLimited bool
}

// spend makes the sign-in starts and the failed token lookups of tc.
func (f budgetFixture) spend(t *testing.T, tc budgetCase) {
	t.Helper()

	for range tc.signIns {
		f.gates.signInGate.Fail(authgate.WithRequest(t.Context(), budgetAddress))
	}

	for range tc.guesses {
		_, err := f.chain.AuthenticateToken(authgate.WithRequest(t.Context(), budgetAddress), guess())
		if !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("guess: err = %v, want ErrUnauthenticated", err)
		}
	}
}

// tokenLimited reports whether the address has spent the budget of the token
// lookups. It reads the budget through the gate with a context that is
// already done, so no timer decides the result: the gate answers
// identity.ErrRateLimited over the budget and nil within it, and the same
// wait of the gate serves the token lookups.
func (f budgetFixture) tokenLimited(t *testing.T) bool {
	t.Helper()

	err := f.gates.gate.Admit(canceledContext(t))
	if err != nil && !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("token admit: err = %v, want nil or ErrRateLimited", err)
	}

	return err != nil
}

// lookupValid looks up the valid token from the address. Call it only within
// the budget. The context has no deadline, so the speed of the store does not
// change the result.
func (f budgetFixture) lookupValid(t *testing.T) error {
	t.Helper()

	_, err := f.chain.AuthenticateToken(authgate.WithRequest(t.Context(), budgetAddress), f.valid)

	return err
}

// TestSignInBudgetIsApartFromTokenLookups proves that the sign-in routes and
// the token lookups keep separate budgets for one address: each limit holds
// on its own, and neither spends the other.
func TestSignInBudgetIsApartFromTokenLookups(t *testing.T) {
	t.Parallel()

	cases := []budgetCase{
		{
			name: "100 sign-in starts leave the token lookups alone", signIns: 100, guesses: 0,
			tokenLimited: false, signInLimited: true,
		},
		{
			name: "a flood of token guesses leaves the sign-in starts alone", signIns: 0,
			guesses: authgate.DefaultBurst, tokenLimited: true, signInLimited: false,
		},
		{
			name: "less than the burst limits nothing", signIns: 5, guesses: 5,
			tokenLimited: false, signInLimited: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newBudgetFixture(t)
			f.spend(t, tc)

			if limited := f.tokenLimited(t); limited != tc.tokenLimited {
				t.Errorf("token budget: limited = %v, want %v", limited, tc.tokenLimited)
			} else if !limited {
				if err := f.lookupValid(t); err != nil {
					t.Errorf("valid token lookup: err = %v, want nil", err)
				}
			}

			err := f.gates.signInGate.Admit(canceledContext(t))
			if limited := errors.Is(err, identity.ErrRateLimited); limited != tc.signInLimited {
				t.Errorf("sign-in admit: err = %v, want limited = %v", err, tc.signInLimited)
			}
		})
	}
}

// TestSignInRoutesUseTheirOwnGate proves the wiring of newSignIn: the limiter
// of the sign-in routes is not the gate of the token lookups. Many sign-in
// starts from one address spend the limiter of newSignIn, and a valid token
// lookup from that address then passes at once. With the token gate as the
// limiter, the same starts spend the token budget, and the budget reads as spent.
func TestSignInRoutesUseTheirOwnGate(t *testing.T) {
	t.Parallel()

	f := newBudgetFixture(t)
	f.cfg.publicURL = "https://dustpan.example"
	f.cfg.devSignIn = &devsignin.Config{Code: nil, Admin: noDevAdmin}

	policies, err := newSessionPolicyService(f.cfg)
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	in, err := newSignIn(f.cfg, policies, nil, f.gates)
	if err != nil {
		t.Fatalf("new sign-in: %v", err)
	}

	if in == nil || in.Limiter == nil {
		t.Fatal("sign-in limiter: nil, want the sign-in gate")
	}

	for range 100 {
		in.Limiter.Fail(authgate.WithRequest(t.Context(), budgetAddress))
	}

	if err = in.Limiter.Admit(canceledContext(t)); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("sign-in admit after 100 starts: err = %v, want ErrRateLimited", err)
	}

	if f.tokenLimited(t) {
		t.Fatal("token budget after 100 sign-in starts: limited, want not limited")
	}

	if err = f.lookupValid(t); err != nil {
		t.Fatalf("valid token lookup after 100 sign-in starts: err = %v, want nil", err)
	}
}
