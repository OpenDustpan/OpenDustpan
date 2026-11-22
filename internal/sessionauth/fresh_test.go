package sessionauth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

type fakeSealer struct {
	claims  session.AccessClaims
	openErr error
}

func (f *fakeSealer) SealAccess(_ context.Context, _ session.AccessClaims) (string, error) {
	return "sealed", nil
}

func (f *fakeSealer) OpenAccess(_ context.Context, raw string, _ time.Time) (session.AccessClaims, error) {
	if f.openErr != nil {
		var none session.AccessClaims

		return none, f.openErr
	}

	if raw == "forged" {
		var none session.AccessClaims

		return none, session.ErrCookieOpen
	}

	return f.claims, nil
}

func (f *fakeSealer) SealRefresh(_ context.Context, _ session.RefreshClaims) (string, error) {
	return "", session.ErrCookieOpen
}

func (f *fakeSealer) OpenRefresh(_ context.Context, _ string) (session.RefreshClaims, error) {
	var none session.RefreshClaims

	return none, session.ErrCookieOpen
}

func (f *fakeSealer) SealForge(_ context.Context, _ session.ForgeClaims) (string, error) {
	return "", session.ErrCookieOpen
}

func (f *fakeSealer) OpenForge(_ context.Context, _, _ string, _ time.Time) (session.ForgeClaims, error) {
	var none session.ForgeClaims

	return none, session.ErrCookieOpen
}

func freshMatch() session.Match {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	return session.Match{
		Session: session.Session{
			CreatedAt:   at,
			LastSeenAt:  at,
			AuthTime:    at,
			EndedAt:     time.Time{},
			ID:          "ses-1",
			PrincipalID: "prn-1",
			Provider:    "corp",
			EndReason:   "",
			SourceIP:    "",
			UserAgent:   "",
			Generation:  3,
		},
		Owner: session.Owner{
			ActiveAt:    time.Time{},
			DisabledAt:  time.Time{},
			ID:          "prn-1",
			Subject:     "corp:alice",
			DisplayName: "Alice",
			Kind:        principal.KindPerson,
			BreakGlass:  false,
		},
		Digest:     session.Digest{},
		Policy:     session.DefaultPolicy(),
		Accounts:   session.AccountPolicy{Inactivity: 0, DisableAccounts: false},
		Generation: 3,
	}
}

func freshClaims(now time.Time, m session.Match) session.AccessClaims {
	claims := session.ClaimsOf(m.Session, session.SignInMethod{
		Method:        session.MethodPasskey,
		AuthTime:      time.Time{},
		AuthTimeKnown: false,
		Factors:       nil,
	}, now)
	// Keep the expiry after the system clock of the checker, which has no
	// fixed clock in this test, so that only the hostile change refuses.
	live := time.Now().UTC().Add(session.AccessLifetime)

	var out session.AccessClaims
	out.Claims = claims
	out.IssuedAt = time.Now().UTC()
	out.ExpiresAt = live
	out.KeyID = ""

	return out
}

func emptyAccessClaims() session.AccessClaims {
	var out session.AccessClaims
	var empty session.Claims
	out.Claims = empty
	out.IssuedAt = time.Time{}
	out.ExpiresAt = time.Time{}
	out.KeyID = ""

	return out
}

func TestCallerFreshHostile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	m := freshMatch()

	tests := []struct {
		name     string
		access   string
		sealed   session.AccessClaims
		openErr  error
		wantKind session.FreshKind
	}{
		{
			name:     "forged value",
			access:   "forged",
			sealed:   emptyAccessClaims(),
			openErr:  nil,
			wantKind: session.FreshNone,
		},
		{
			name:     "missing cookie",
			access:   "",
			sealed:   emptyAccessClaims(),
			openErr:  nil,
			wantKind: session.FreshNone,
		},
		{
			name:     "valid cookie gives a fact",
			access:   "sealed",
			sealed:   freshClaims(now, m),
			openErr:  nil,
			wantKind: session.FreshPasskey,
		},
		{
			name:   "cookie of another person",
			access: "sealed",
			sealed: func() session.AccessClaims {
				c := freshClaims(now, m)
				c.PrincipalID = "prn-2"

				return c
			}(),
			openErr:  nil,
			wantKind: session.FreshNone,
		},
		{
			name:   "expired cookie",
			access: "sealed",
			sealed: func() session.AccessClaims {
				c := freshClaims(now, m)
				c.ExpiresAt = time.Now().UTC().Add(-time.Second)

				return c
			}(),
			openErr:  nil,
			wantKind: session.FreshNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeSessions{match: m, err: nil, calls: 0}
			sealer := &fakeSealer{claims: tt.sealed, openErr: tt.openErr}

			c, err := sessionauth.New(fake, []string{consoleOrigin}, sessionauth.WithSealer(sealer))
			if err != nil {
				t.Fatalf("new checker: %v", err)
			}

			secret := newSecret(t)
			fake.match = m

			r := request(t, "GET", secret.Reveal(), map[string]string{"Sec-Fetch-Site": "same-origin"})
			if tt.access != "" {
				r.AddCookie(&http.Cookie{
					Name: session.AccessCookie, Value: tt.access, Path: "/", Secure: true, HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
				})
			}

			_, caller, err := c.Authenticate(r)
			if err != nil {
				t.Fatalf("Authenticate = %v, want nil", err)
			}

			if caller.Fresh.Kind != tt.wantKind {
				t.Errorf("Fresh.Kind = %q, want %q", caller.Fresh.Kind, tt.wantKind)
			}
		})
	}
}
