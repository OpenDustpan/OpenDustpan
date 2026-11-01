package tfe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// aliceSessions accepts every session as the session of corp:alice. The
// sign-in of the session is age old: a negative age is a sign-in after now.
type aliceSessions struct {
	age time.Duration
}

func (a aliceSessions) Authenticate(context.Context, session.Secret) (session.Match, error) {
	return session.Match{
		Session: session.Session{
			CreatedAt: time.Time{}, LastSeenAt: time.Time{}, AuthTime: time.Now().Add(-a.age), EndedAt: time.Time{},
			ID: "ses-1", PrincipalID: "prn-1", Provider: "corp", EndReason: "", SourceIP: "", UserAgent: "",
			Generation: 1,
		},
		Owner: session.Owner{
			ActiveAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-1", Subject: "corp:alice", DisplayName: "Alice",
			Kind: principal.KindPerson, BreakGlass: false,
		},
		Digest:     session.Digest{},
		Policy:     session.DefaultPolicy(),
		Accounts:   session.AccountPolicy{Inactivity: 0, DisableAccounts: false},
		Generation: 1,
	}, nil
}

// aliceChecker returns a session cookie check that takes every session as
// the session of corp:alice, who signed in age ago.
func aliceChecker(t *testing.T, age time.Duration) *sessionauth.Checker {
	t.Helper()

	c, err := sessionauth.New(aliceSessions{age: age}, []string{"https://dustpan.example"})
	if err != nil {
		t.Fatalf("session check: %v", err)
	}

	return c
}

// sessionPost returns a POST request with a console session cookie, the CSRF
// value of the session and no Authorization header.
func sessionPost(t *testing.T, target, body string) *http.Request {
	t.Helper()

	secret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), "req-session"), http.MethodPost, target, strings.NewReader(body),
	)
	req.AddCookie(&http.Cookie{
		Name: sessionauth.Cookie, Value: secret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(secret))
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	return req
}
