package postgres_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestPolicyChangeThroughOneHandleAppliesToTheOther proves that a token
// policy change that one store handle writes applies at once to a token
// lookup through another handle on the same schema: two instances of the
// server share one database, and no instance keeps an old policy.
func TestPolicyChangeThroughOneHandleAppliesToTheOther(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the two-handle policy test", envURL)
	}

	ctx := t.Context()
	schemaURL := isolatedURL(t, base)

	// The clock starts the inactivity period and one hour before the real
	// time, so each record of the test is in the past of the database clock.
	now := time.Now().UTC().Truncate(time.Millisecond).Add(-accesstoken.DefaultInactivity - time.Hour)
	clock := func() time.Time { return now }

	first := openHandle(t, schemaURL)
	second := openHandle(t, schemaURL)

	writer := tokenService(t, first, clock)
	reader := tokenService(t, second, clock)

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      first,
			Guard:      principal.AdminOnlyGuard{},
			GroupReach: nil,
			Namespaces: nil,
			Tokens:     nil,
			Now:        clock,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	owner, err := principals.CreatePrincipal(ctx, policyCaller(),
		principal.Input{Subject: "ci-deploy", DisplayName: "", Kind: principal.KindService})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	admin := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "admin", DisplayName: "admin", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}

	secrets := make([]accesstoken.Secret, 0, 2)

	for _, name := range []string{"before", "after"} {
		_, secret, createErr := writer.Create(ctx, admin, policyCaller(), accesstoken.ScopeService, accesstoken.Input{
			ExpiresAt: now.Add(200 * 24 * time.Hour), PrincipalID: owner.ID, Name: name,
		})
		if createErr != nil {
			t.Fatalf("create token %q: %v", name, createErr)
		}

		secrets = append(secrets, secret)
	}

	setDisable := func(on bool) {
		t.Helper()

		_, setErr := writer.SetPolicy(ctx, policyCaller(), accesstoken.PolicyChange{
			MaxLifetime: nil, Inactivity: nil, DisableTokens: new(on), DisableAccounts: nil, DisableBreakGlass: nil,
			ExchangeLifetime: nil,
		})
		if setErr != nil {
			t.Fatalf("set policy through the first handle: %v", setErr)
		}
	}

	setDisable(false)

	now = now.Add(accesstoken.DefaultInactivity + time.Minute)

	if _, authErr := reader.AuthenticateToken(ctx, secrets[0].Reveal()); authErr != nil {
		t.Fatalf("inactive token with the disable off, through the second handle: %v", authErr)
	}

	setDisable(true)

	_, authErr := reader.AuthenticateToken(ctx, secrets[1].Reveal())
	if !errors.Is(authErr, identity.ErrUnauthenticated) {
		t.Fatalf("inactive token right after the change through the first handle: err = %v, want ErrUnauthenticated",
			authErr)
	}
}

// openHandle opens one more store handle on the schema at schemaURL.
func openHandle(t *testing.T, schemaURL string) *postgres.Store {
	t.Helper()

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// tokenService returns a token service on the store handle s, with the
// clock now.
func tokenService(t *testing.T, s *postgres.Store, now func() time.Time) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(accesstoken.Config{
		Reach: access.ReachConfig{
			Grants:      nil,
			Roles:       nil,
			Callers:     nil,
			Principals:  nil,
			Collections: nil,
			Units:       nil,
		},
		TempAdmins: nil,
		Store:      s,
		Principals: s,
		Freshness:  nil,
		Authorizer: nil,
		Logger:     nil,
		Now:        now,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return svc
}

// policyCaller is the audit entry of the administrator that changes the
// principals, the tokens and the policy of the test.
func policyCaller() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "",
		Action: string(identity.ActionServerManageTokens), Object: "", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "",
		Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}
