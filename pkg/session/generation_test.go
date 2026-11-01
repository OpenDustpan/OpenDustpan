package session_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// generationStore is a store whose lookup changes the generations that the
// database answered, as a changed row or a bump between two reads would.
type generationStore struct {
	*sqlite.Store

	sessionGeneration int64
	storedGeneration  int64
}

func (g generationStore) GetSessionByDigest(ctx context.Context, d session.Digest) (session.Match, error) {
	m, err := g.Store.GetSessionByDigest(ctx, d)
	if err != nil {
		return m, err
	}

	m.Session.Generation = g.sessionGeneration
	m.Generation = g.storedGeneration

	return m, nil
}

// TestAuthenticateRefusesAnotherGeneration sends hostile generations
// directly to the check of Authenticate, as if the store had passed them: a
// session whose generation is not the stored generation of its owner, and a
// generation that no principal has, never authenticate.
func TestAuthenticateRefusesAnotherGeneration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		session, stored int64
		wantErr         bool
	}{
		{name: "same generation", session: 3, stored: 3, wantErr: false},
		{name: "older session", session: 2, stored: 3, wantErr: true},
		{name: "newer session", session: 4, stored: 3, wantErr: true},
		{name: "zero on both sides", session: 0, stored: 0, wantErr: true},
		{name: "negative on both sides", session: -1, stored: -1, wantErr: true},
		{name: "zero session from before the column", session: 0, stored: 1, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newSessionsFixture(t)
			_, secret := f.start(t)

			svc, err := session.NewSessions(session.SessionsConfig{
				Store:     generationStore{Store: f.store, sessionGeneration: tc.session, storedGeneration: tc.stored},
				Providers: nil, Inactivity: nil, Now: f.clock.Now,
			})
			if err != nil {
				t.Fatalf("sessions: %v", err)
			}

			_, err = svc.Authenticate(t.Context(), secret)

			switch {
			case tc.wantErr && !errors.Is(err, session.ErrUnauthenticated):
				t.Fatalf("authenticate = %v, want ErrUnauthenticated", err)
			case tc.wantErr && !errors.Is(err, session.ErrGenerationChanged):
				t.Fatalf("authenticate = %v, want ErrGenerationChanged", err)
			case !tc.wantErr && err != nil:
				t.Fatalf("authenticate = %v, want nil", err)
			default:
			}
		})
	}
}
