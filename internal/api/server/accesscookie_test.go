package server_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

func accessTestSession() session.Session {
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

func accessTestMethod(kind session.FreshKind, now time.Time) session.SignInMethod {
	if kind == session.FreshPasskey {
		return session.SignInMethod{
			Method:        session.MethodPasskey,
			AuthTime:      time.Time{},
			AuthTimeKnown: false,
			Factors:       nil,
		}
	}

	if kind == session.FreshPassword {
		return session.SignInMethod{
			Method:        session.MethodLocalPassword,
			AuthTime:      time.Time{},
			AuthTimeKnown: false,
			Factors:       []session.Factor{session.FactorPassword},
		}
	}

	if kind == session.FreshMFA {
		return session.SignInMethod{
			Method:        session.MethodLDAP,
			AuthTime:      time.Time{},
			AuthTimeKnown: false,
			Factors:       []session.Factor{session.FactorPassword, session.FactorTOTP},
		}
	}

	if kind == session.FreshSSO {
		return session.SignInMethod{
			Method:        session.MethodOIDC,
			AuthTime:      now.Add(-time.Minute),
			AuthTimeKnown: true,
			Factors:       nil,
		}
	}

	return session.SignInMethod{
		Method:        "",
		AuthTime:      time.Time{},
		AuthTimeKnown: false,
		Factors:       nil,
	}
}

func TestSignInSetsAccessCookie(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(session.AccessCookie, "__Host-") {
		t.Errorf("AccessCookie = %q, want __Host- prefix", session.AccessCookie)
	}

	if session.AccessLifetime != 5*time.Minute {
		t.Errorf("AccessLifetime = %v, want 5m", session.AccessLifetime)
	}

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	sess := accessTestSession()

	kinds := []struct {
		name string
		want session.FreshKind
	}{
		{name: "passkey", want: session.FreshPasskey},
		{name: "password", want: session.FreshPassword},
		{name: "mfa", want: session.FreshMFA},
		{name: "sso", want: session.FreshSSO},
		{name: "dev gives no kind", want: session.FreshNone},
	}

	for _, tt := range kinds {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := session.ClaimsOf(sess, accessTestMethod(tt.want, now), now)
			if got.FreshKind != tt.want {
				t.Errorf("FreshKind = %q, want %q", got.FreshKind, tt.want)
			}

			if tt.want != session.FreshNone && got.FreshAt.IsZero() {
				t.Error("FreshAt is zero, want a time")
			}
		})
	}
}

func TestAccessCookieHandlerHostile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	sess := accessTestSession()

	t.Run("failed sign-in sets no cookie", func(t *testing.T) {
		t.Parallel()

		got := session.ClaimsOf(sess, session.SignInMethod{
			Method:        "",
			AuthTime:      time.Time{},
			AuthTimeKnown: false,
			Factors:       nil,
		}, now)
		if got.FreshKind != session.FreshNone {
			t.Errorf("FreshKind = %q, want empty", got.FreshKind)
		}
	})

	t.Run("token gets no cookie", func(t *testing.T) {
		t.Parallel()

		if session.AccessCookie == "" {
			t.Error("AccessCookie is empty")
		}
	})

	t.Run("agent gets no cookie", func(t *testing.T) {
		t.Parallel()

		got := session.ClaimsOf(sess, session.SignInMethod{
			Method:        "",
			AuthTime:      time.Time{},
			AuthTimeKnown: false,
			Factors:       nil,
		}, now)
		if got.FreshKind != session.FreshNone {
			t.Errorf("agent fact = %q, want empty", got.FreshKind)
		}
	})
}

func TestReissueKeepsFreshAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	sess := accessTestSession()
	claims := session.ClaimsOf(sess, accessTestMethod(session.FreshPasskey, now), now)

	var opened session.AccessClaims
	opened.Claims = claims
	opened.IssuedAt = now
	opened.ExpiresAt = now.Add(session.AccessLifetime)
	opened.KeyID = ""

	renewed := opened
	renewed.IssuedAt = now.Add(time.Minute)
	renewed.ExpiresAt = renewed.IssuedAt.Add(session.AccessLifetime)

	if renewed.FreshAt != opened.FreshAt {
		t.Error("FreshAt moved")
	}

	if renewed.FreshKind != opened.FreshKind {
		t.Errorf("FreshKind = %q, want %q", renewed.FreshKind, opened.FreshKind)
	}

	if !renewed.ExpiresAt.After(opened.ExpiresAt) {
		t.Error("ExpiresAt did not move")
	}
}

func TestReissueOnlyAcceptedCookie(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	sess := accessTestSession()

	var match session.Match
	match.Session = sess
	match.Owner.ID = sess.PrincipalID
	match.Owner.Subject = "corp:alice"
	match.Owner.Kind = "person"
	match.Generation = sess.Generation
	match.Policy = session.DefaultPolicy()
	match.Accounts = session.AccountPolicy{Inactivity: 0, DisableAccounts: false}
	match.Digest = session.Digest{}

	cases := []struct {
		name string
		kind session.FreshKind
	}{
		{name: "forged", kind: "other"},
		{name: "missing", kind: session.FreshNone},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			claims := session.ClaimsOf(sess, accessTestMethod(session.FreshPasskey, now), now)
			claims.FreshKind = tt.kind

			var c session.AccessClaims
			c.Claims = claims
			c.IssuedAt = now
			c.ExpiresAt = now.Add(session.AccessLifetime)
			c.KeyID = ""

			var none session.FreshFact
			if got := session.BindFact(c, match, now); got != none {
				t.Errorf("BindFact = %+v, want zero", got)
			}
		})
	}
}

func TestSSOAuthTimeHostile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	sess := accessTestSession()

	hostile := []struct {
		name string
		how  session.SignInMethod
	}{
		{
			name: "unknown",
			how: session.SignInMethod{
				Method:        session.MethodOIDC,
				AuthTime:      time.Time{},
				AuthTimeKnown: false,
				Factors:       nil,
			},
		},
		{
			name: "zero",
			how: session.SignInMethod{
				Method:        session.MethodOIDC,
				AuthTime:      time.Time{},
				AuthTimeKnown: true,
				Factors:       nil,
			},
		},
		{
			name: "future",
			how: session.SignInMethod{
				Method:        session.MethodOIDC,
				AuthTime:      now.Add(time.Minute),
				AuthTimeKnown: true,
				Factors:       nil,
			},
		},
		{
			name: "older than window",
			how: session.SignInMethod{
				Method:        session.MethodOIDC,
				AuthTime:      now.Add(-session.MaxTokenFreshness - time.Minute),
				AuthTimeKnown: true,
				Factors:       nil,
			},
		},
	}

	for _, tt := range hostile {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := session.ClaimsOf(sess, tt.how, now)
			if got.FreshKind != session.FreshNone {
				t.Errorf("FreshKind = %q, want empty", got.FreshKind)
			}

			if !got.FreshAt.IsZero() {
				t.Errorf("FreshAt = %v, want zero", got.FreshAt)
			}
		})
	}
}

func TestRenewSessionNoFreshFact(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)
	sess := accessTestSession()

	got := session.ClaimsOf(sess, session.SignInMethod{
		Method:        "",
		AuthTime:      time.Time{},
		AuthTimeKnown: false,
		Factors:       nil,
	}, now)
	if got.FreshKind != session.FreshNone {
		t.Errorf("renewal kind = %q, want empty", got.FreshKind)
	}

	if !got.FreshAt.IsZero() || !got.MFAAt.IsZero() {
		t.Errorf("renewal times = %v %v, want zero", got.FreshAt, got.MFAAt)
	}
}
