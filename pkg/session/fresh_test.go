package session_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

func testSession() session.Session {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	return session.Session{
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
	}
}

func testMatch() session.Match {
	s := testSession()

	return session.Match{
		Session: s,
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

func passkeyMethod() session.SignInMethod {
	return session.SignInMethod{
		Method:        session.MethodPasskey,
		AuthTime:      time.Time{},
		AuthTimeKnown: false,
		Factors:       nil,
	}
}

func TestClaimsOf(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	s := testSession()

	tests := []struct {
		name      string
		how       session.SignInMethod
		wantKind  session.FreshKind
		wantFresh bool
		wantMFA   bool
	}{
		{
			name:      "passkey",
			how:       passkeyMethod(),
			wantKind:  session.FreshPasskey,
			wantFresh: true,
			wantMFA:   true,
		},
		{
			name: "password alone",
			how: session.SignInMethod{
				Method:        session.MethodLocalPassword,
				AuthTime:      time.Time{},
				AuthTimeKnown: false,
				Factors:       []session.Factor{session.FactorPassword},
			},
			wantKind:  session.FreshPassword,
			wantFresh: true,
			wantMFA:   false,
		},
		{
			name: "password with second factor",
			how: session.SignInMethod{
				Method:        session.MethodLDAP,
				AuthTime:      time.Time{},
				AuthTimeKnown: false,
				Factors:       []session.Factor{session.FactorPassword, session.FactorTOTP},
			},
			wantKind:  session.FreshMFA,
			wantFresh: true,
			wantMFA:   true,
		},
		{
			name: "sso",
			how: session.SignInMethod{
				Method:        session.MethodOIDC,
				AuthTime:      now.Add(-time.Minute),
				AuthTimeKnown: true,
				Factors:       nil,
			},
			wantKind:  session.FreshSSO,
			wantFresh: true,
			wantMFA:   false,
		},
		{
			name: "dev",
			how: session.SignInMethod{
				Method:        "",
				AuthTime:      time.Time{},
				AuthTimeKnown: false,
				Factors:       nil,
			},
			wantKind:  session.FreshNone,
			wantFresh: false,
			wantMFA:   false,
		},
		{
			name: "oauth never gives a fact",
			how: session.SignInMethod{
				Method:        session.MethodOAuth,
				AuthTime:      now.Add(-time.Minute),
				AuthTimeKnown: true,
				Factors:       nil,
			},
			wantKind:  session.FreshNone,
			wantFresh: false,
			wantMFA:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := session.ClaimsOf(s, tt.how, now)
			checkClaims(t, got, s, tt.wantKind)
		})
	}
}

func checkClaims(t *testing.T, got session.Claims, s session.Session, wantKind session.FreshKind) {
	t.Helper()

	wantFresh := wantKind != session.FreshNone
	wantMFA := wantKind == session.FreshPasskey || wantKind == session.FreshMFA

	if got.FreshKind != wantKind {
		t.Errorf("ClaimsOf kind = %q, want %q", got.FreshKind, wantKind)
	}

	if wantFresh && got.FreshAt.IsZero() {
		t.Error("ClaimsOf FreshAt is zero, want a time")
	}

	if !wantFresh && !got.FreshAt.IsZero() {
		t.Errorf("ClaimsOf FreshAt = %v, want zero", got.FreshAt)
	}

	if wantMFA && got.MFAAt.IsZero() {
		t.Error("ClaimsOf MFAAt is zero, want a time")
	}

	if got.PrincipalID != s.PrincipalID {
		t.Errorf("ClaimsOf PrincipalID = %q, want %q", got.PrincipalID, s.PrincipalID)
	}

	if got.Generation != s.Generation {
		t.Errorf("ClaimsOf Generation = %d, want %d", got.Generation, s.Generation)
	}

	if !got.SignInAt.Equal(s.CreatedAt) {
		t.Errorf("ClaimsOf SignInAt = %v, want %v", got.SignInAt, s.CreatedAt)
	}
}

func testGoodClaims(now time.Time, m session.Match) session.AccessClaims {
	claims := session.ClaimsOf(m.Session, passkeyMethod(), now)

	var good session.AccessClaims
	good.Claims = claims
	good.IssuedAt = now
	good.ExpiresAt = now.Add(session.AccessLifetime)
	good.KeyID = ""

	return good
}

func TestBindFact(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	m := testMatch()
	good := testGoodClaims(now, m)

	got := session.BindFact(good, m, now)
	if got.Kind != session.FreshPasskey {
		t.Fatalf("BindFact kind = %q, want passkey", got.Kind)
	}

	if !got.At.Equal(good.FreshAt) {
		t.Errorf("BindFact At = %v, want %v", got.At, good.FreshAt)
	}

	mismatches := []struct {
		name   string
		change func(*session.AccessClaims)
	}{
		{name: "other person", change: func(c *session.AccessClaims) { c.PrincipalID = "prn-2" }},
		{name: "older generation", change: func(c *session.AccessClaims) { c.Generation = 2 }},
		{name: "other sign-in", change: func(c *session.AccessClaims) {
			c.SignInAt = m.Session.CreatedAt.Add(time.Second)
		}},
		{name: "expired", change: func(c *session.AccessClaims) { c.ExpiresAt = now.Add(-time.Second) }},
		{name: "empty kind", change: func(c *session.AccessClaims) { c.FreshKind = session.FreshNone }},
	}

	for _, tt := range mismatches {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := good
			tt.change(&c)

			var none session.FreshFact
			if got := session.BindFact(c, m, now); got != none {
				t.Errorf("BindFact = %+v, want zero", got)
			}
		})
	}
}

func TestBindFactHostile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	m := testMatch()
	good := testGoodClaims(now, m)

	hostile := []struct {
		name   string
		change func(*session.AccessClaims)
	}{
		{name: "empty principal", change: func(c *session.AccessClaims) { c.PrincipalID = "" }},
		{name: "zero generation", change: func(c *session.AccessClaims) { c.Generation = 0 }},
		{name: "zero sign-in time", change: func(c *session.AccessClaims) { c.SignInAt = time.Time{} }},
		{name: "unknown kind", change: func(c *session.AccessClaims) { c.FreshKind = "other" }},
		{name: "future fresh time", change: func(c *session.AccessClaims) {
			c.FreshAt = now.Add(time.Minute)
		}},
		{name: "future mfa time", change: func(c *session.AccessClaims) { c.MFAAt = now.Add(time.Minute) }},
		{name: "missing expiry", change: func(c *session.AccessClaims) { c.ExpiresAt = time.Time{} }},
		{name: "zero fresh time", change: func(c *session.AccessClaims) { c.FreshAt = time.Time{} }},
	}

	for _, tt := range hostile {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := good
			tt.change(&c)

			var none session.FreshFact
			if got := session.BindFact(c, m, now); got != none {
				t.Errorf("BindFact = %+v, want zero", got)
			}
		})
	}
}
