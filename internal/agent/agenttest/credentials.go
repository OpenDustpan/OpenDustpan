package agenttest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
)

// CredentialsHarness is one fresh identity service for one case of the
// credentials suite. Each function makes a new principal with the subject
// that it returns and one credential of that principal, and returns the ID
// of the credential.
type CredentialsHarness struct {
	Credentials agent.Credentials
	// Token makes a personal access token that is valid now.
	Token func(t *testing.T) (subject, credentialID string)
	// ServiceToken makes a service token that is valid now: its principal
	// cannot sign in to the console.
	ServiceToken func(t *testing.T) (subject, credentialID string)
	// Session makes a console session that is valid now.
	Session func(t *testing.T) (subject, credentialID string)
	// BreakGlass makes a break-glass session that is valid now.
	BreakGlass func(t *testing.T) (subject, credentialID string)
	// Revoke revokes the credential, as a person does.
	Revoke func(t *testing.T, credentialID string)
	// Expire moves the credential past its expiry, or past the time limit
	// of a break-glass session.
	Expire func(t *testing.T, credentialID string)
	// EndSession signs the session out.
	EndSession func(t *testing.T, credentialID string)
	// Disable disables the principal subject.
	Disable func(t *testing.T, subject string)
}

// CredentialsFactory returns a fresh harness for one case.
type CredentialsFactory func(t *testing.T) CredentialsHarness

// RunCredentials runs the credentials suite against the identity service
// that f makes.
func RunCredentials(t *testing.T, f CredentialsFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h CredentialsHarness)
	}{
		{name: "valid token and session", run: caseCredentialsValid},
		{name: "break-glass session", run: caseCredentialsBreakGlass},
		{name: "service token", run: caseCredentialsService},
		{name: "revoked token", run: caseCredentialsRevoked},
		{name: "expired credentials", run: caseCredentialsExpired},
		{name: "ended session", run: caseCredentialsSessionEnded},
		{name: "disabled principal", run: caseCredentialsDisabled},
		{name: "credential of another subject", run: caseCredentialsOtherSubject},
		{name: "unknown credential", run: caseCredentialsUnknown},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

// wantEnded fails t unless the credential resolves to ErrCredentialEnded.
func wantEnded(t *testing.T, h CredentialsHarness, what, subject, credentialID string) {
	t.Helper()

	got, err := h.Credentials.Resolve(t.Context(), subject, credentialID)
	if !errors.Is(err, agent.ErrCredentialEnded) {
		t.Fatalf("Resolve() of %s = %+v, %v, want ErrCredentialEnded", what, got, err)
	}
}

func caseCredentialsValid(t *testing.T, h CredentialsHarness) {
	t.Helper()

	for what, mk := range map[string]func(*testing.T) (string, string){"token": h.Token, "session": h.Session} {
		subject, id := mk(t)

		got, err := h.Credentials.Resolve(t.Context(), subject, id)
		if err != nil || got.Principal.Subject != subject || got.BreakGlass || got.ServicePrincipal {
			t.Fatalf("Resolve() of a %s = %+v, %v, want the person %q, not break-glass", what, got, err, subject)
		}
	}
}

func caseCredentialsBreakGlass(t *testing.T, h CredentialsHarness) {
	t.Helper()

	subject, id := h.BreakGlass(t)

	got, err := h.Credentials.Resolve(t.Context(), subject, id)
	if err != nil || !got.BreakGlass {
		t.Fatalf("Resolve() of a break-glass session = %+v, %v, want BreakGlass", got, err)
	}

	h.Expire(t, id)
	wantEnded(t, h, "a break-glass session past its time limit", subject, id)
}

func caseCredentialsService(t *testing.T, h CredentialsHarness) {
	t.Helper()

	subject, id := h.ServiceToken(t)

	got, err := h.Credentials.Resolve(t.Context(), subject, id)
	if err != nil || got.Principal.Subject != subject || !got.ServicePrincipal {
		t.Fatalf("Resolve() of a service token = %+v, %v, want ServicePrincipal", got, err)
	}
}

func caseCredentialsRevoked(t *testing.T, h CredentialsHarness) {
	t.Helper()

	subject, id := h.Token(t)
	h.Revoke(t, id)
	wantEnded(t, h, "a revoked token", subject, id)
}

func caseCredentialsExpired(t *testing.T, h CredentialsHarness) {
	t.Helper()

	for _, mk := range []func(*testing.T) (string, string){h.Token, h.Session} {
		subject, id := mk(t)
		h.Expire(t, id)
		wantEnded(t, h, "an expired credential", subject, id)
	}
}

func caseCredentialsSessionEnded(t *testing.T, h CredentialsHarness) {
	t.Helper()

	subject, id := h.Session(t)
	h.EndSession(t, id)
	wantEnded(t, h, "a session that signed out", subject, id)
}

func caseCredentialsDisabled(t *testing.T, h CredentialsHarness) {
	t.Helper()

	subject, id := h.Token(t)
	h.Disable(t, subject)
	wantEnded(t, h, "a token of a disabled principal", subject, id)
}

func caseCredentialsOtherSubject(t *testing.T, h CredentialsHarness) {
	t.Helper()

	_, id := h.Token(t)
	other, _ := h.Token(t)
	wantEnded(t, h, "a token for another subject", other, id)
}

func caseCredentialsUnknown(t *testing.T, h CredentialsHarness) {
	t.Helper()

	subject, _ := h.Token(t)
	wantEnded(t, h, "an unknown credential", subject, "no-such-credential")
}
