package localusersvc_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestBumpEndsLiveSession proves the session generation end to end on one
// store: a session that a password sign-in starts authenticates, a reset adds
// 1 to the generation and the session ends at its next request, and a sign-in
// that read the generation before the reset cannot start a session.
func TestBumpEndsLiveSession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	sessions, err := session.NewSessions(session.SessionsConfig{
		Store: f.store, Providers: nil, Inactivity: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	in, err := f.svc.SignIn(t.Context(), base(""), "alice", goodPassword)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	start := session.Start{
		AuthTime: sessions.Now(), PrincipalID: in.PrincipalID, Provider: localuser.ProviderID,
		SourceIP: "192.0.2.1", UserAgent: "test", Generation: in.SessionGeneration, Stored: false, Alert: nil,
	}

	e := base(c.Principal.Subject)
	e.Action = session.ActionSignIn

	_, secret, err := sessions.Start(t.Context(), e, start)
	if err != nil {
		t.Fatalf("start the session: %v", err)
	}

	if _, authErr := sessions.Authenticate(t.Context(), secret); authErr != nil {
		t.Fatalf("authenticate before the reset: %v", authErr)
	}

	_, err = f.svc.StartReset(t.Context(), admin(), c.Principal.ID, localusersvc.ResetInput{
		Reason: "the person lost the password", RevokeAccessTokens: false,
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}

	_, err = sessions.Authenticate(t.Context(), secret)
	if !errors.Is(err, session.ErrUnauthenticated) || !errors.Is(err, session.ErrGenerationChanged) {
		t.Fatalf("authenticate after the reset = %v, want ErrGenerationChanged", err)
	}

	// A sign-in that read the generation before the reset races it: its
	// session is refused in the write that would store it.
	if _, _, raceErr := sessions.Start(t.Context(), e, start); !errors.Is(raceErr, session.ErrGenerationChanged) {
		t.Fatalf("start with the old generation = %v, want ErrGenerationChanged", raceErr)
	}
}
