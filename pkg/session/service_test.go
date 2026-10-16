package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// clock is a clock that a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// sessionsFixture is a Sessions service over a SQLite store, with one
// person principal.
type sessionsFixture struct {
	store    *sqlite.Store
	policies *memoryPolicies
	svc      *session.Sessions
	clock    *clock
}

const personID = "prn-alice"

func newSessionsFixture(t *testing.T) sessionsFixture {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	c := &clock{mu: sync.Mutex{}, now: time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)}
	at := c.Now().Add(-time.Hour)

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: personID, Subject: "corp:alice",
		DisplayName: "Alice", Kind: principal.KindPerson, Source: principal.SourceOIDC, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	pe := signInEntry()
	pe.At, pe.Namespace, pe.Kind, pe.Outcome, pe.Object = at, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, principal.Ref(personID)

	if createErr := st.CreatePrincipal(t.Context(), p, pe); createErr != nil {
		t.Fatalf("create principal: %v", createErr)
	}

	policies := &memoryPolicies{policy: session.DefaultPolicy(), entries: nil}

	pol, err := session.NewPolicies(session.PolicyConfig{Store: policies, Now: c.Now})
	if err != nil {
		t.Fatalf("policies: %v", err)
	}

	svc, err := session.NewSessions(session.SessionsConfig{Store: st, Policies: pol, Now: c.Now})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	return sessionsFixture{store: st, policies: policies, svc: svc, clock: c}
}

// signInEntry is the entry of the caller of a sign-in.
func signInEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "corp:alice", IDPSubject: "alice", TokenID: "",
		Action: session.ActionSignIn, Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: "", Outcome: "",
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0, ObjectID: "",
	}
}

func startInput(authTime time.Time) session.Start {
	return session.Start{
		AuthTime: authTime, PrincipalID: personID, Provider: "corp", SourceIP: "192.0.2.1", UserAgent: "test",
	}
}

func (f sessionsFixture) start(t *testing.T) (session.Session, session.Secret) {
	t.Helper()

	sess, secret, err := f.svc.Start(t.Context(), signInEntry(), startInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return sess, secret
}

func TestSessionsStartAndAuthenticate(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	sess, secret := f.start(t)

	if !strings.HasPrefix(sess.ID, "ses-") || sess.PrincipalID != personID || sess.Provider != "corp" {
		t.Errorf("session = %+v", sess)
	}

	m, err := f.svc.Authenticate(t.Context(), secret)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if m.Session.ID != sess.ID || m.Owner.ID != personID || m.Owner.Subject != "corp:alice" {
		t.Errorf("match = %+v", m)
	}

	other, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	if _, err := f.svc.Authenticate(t.Context(), other); !errors.Is(err, session.ErrUnauthenticated) {
		t.Errorf("authenticate an unknown secret = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionsStartWritesAudit(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	sess, _ := f.start(t)

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	for _, e := range listing.Entries {
		if e.Object == session.FullRef(personID, sess.ID) && e.Action == session.ActionSignIn &&
			e.Outcome == audit.OutcomeSuccess {
			return
		}
	}

	t.Fatalf("no sign-in entry for %s in %+v", sess.ID, listing.Entries)
}

func TestSessionsStartClampsAuthTime(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)

	sess, _, err := f.svc.Start(t.Context(), signInEntry(), startInput(f.clock.Now().Add(30*time.Second)))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if !sess.AuthTime.Equal(sess.CreatedAt) {
		t.Errorf("AuthTime = %v, want the start %v", sess.AuthTime, sess.CreatedAt)
	}

	earlier := f.clock.Now().Add(-time.Hour)

	sess, _, err = f.svc.Start(t.Context(), signInEntry(), startInput(earlier))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if !sess.AuthTime.Equal(earlier) {
		t.Errorf("AuthTime = %v, want %v", sess.AuthTime, earlier)
	}
}

func TestSessionsIdle(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	_, secret := f.start(t)

	// Uses every 10 minutes keep the session alive past the idle limit.
	for range 3 {
		f.clock.Add(10 * time.Minute)

		if _, err := f.svc.Authenticate(t.Context(), secret); err != nil {
			t.Fatalf("authenticate after 10 minutes: %v", err)
		}
	}

	f.clock.Add(session.DefaultIdle)

	if _, err := f.svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate after the idle limit = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionsMaxLength(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	_, secret := f.start(t)

	for elapsed := 10 * time.Minute; elapsed < session.DefaultMaxLength; elapsed += 10 * time.Minute {
		f.clock.Add(10 * time.Minute)

		if _, err := f.svc.Authenticate(t.Context(), secret); err != nil {
			t.Fatalf("authenticate after %s: %v", elapsed, err)
		}
	}

	f.clock.Add(10 * time.Minute)

	if _, err := f.svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate after the maximum length = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionsPolicyApplies(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	_, secret := f.start(t)

	f.policies.policy.Limits = session.Limits{Idle: session.MinIdle, MaxLength: time.Hour}
	f.clock.Add(session.MinIdle)

	if _, err := f.svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate after the new idle limit = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionsTouchEvery(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	sess, secret := f.start(t)

	f.clock.Add(30 * time.Second)

	if _, err := f.svc.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	got, err := f.store.GetSession(t.Context(), sess.ID)
	if err != nil || !got.LastSeenAt.Equal(sess.CreatedAt) {
		t.Fatalf("last use after 30 seconds = %v, %v; want %v", got.LastSeenAt, err, sess.CreatedAt)
	}

	f.clock.Add(time.Minute)

	if _, authErr := f.svc.Authenticate(t.Context(), secret); authErr != nil {
		t.Fatalf("authenticate: %v", authErr)
	}

	got, err = f.store.GetSession(t.Context(), sess.ID)
	if err != nil || !got.LastSeenAt.Equal(f.svc.Now()) {
		t.Fatalf("last use after 90 seconds = %v, %v; want %v", got.LastSeenAt, err, f.svc.Now())
	}
}

func TestSessionsSignOut(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	sess, secret := f.start(t)

	e := signInEntry()
	e.Action = session.ActionSignOut

	if err := f.svc.SignOut(t.Context(), e, personID, sess.ID); err != nil {
		t.Fatalf("sign out: %v", err)
	}

	if _, err := f.svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate after sign-out = %v, want ErrUnauthenticated", err)
	}

	if err := f.svc.SignOut(t.Context(), e, personID, sess.ID); !errors.Is(err, session.ErrEnded) {
		t.Fatalf("second sign-out = %v, want ErrEnded", err)
	}
}

// disabledOwner answers each session with a disabled owner.
type disabledOwner struct {
	*sqlite.Store
}

func (s disabledOwner) GetSessionByDigest(ctx context.Context, d session.Digest) (session.Match, error) {
	m, err := s.Store.GetSessionByDigest(ctx, d)
	m.Owner.DisabledAt = m.Session.CreatedAt

	return m, err
}

func TestSessionsRefuseDisabledOwner(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	_, secret := f.start(t)

	pol, err := session.NewPolicies(session.PolicyConfig{Store: f.policies, Now: f.clock.Now})
	if err != nil {
		t.Fatalf("policies: %v", err)
	}

	svc, err := session.NewSessions(
		session.SessionsConfig{Store: disabledOwner{f.store}, Policies: pol, Now: f.clock.Now},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	if _, err := svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate with a disabled owner = %v, want ErrUnauthenticated", err)
	}
}

func begin(t *testing.T, returnPath string) (session.Secret, session.Begin) {
	t.Helper()

	state, err := session.NewSecret()
	if err != nil {
		t.Fatalf("state: %v", err)
	}

	return state, session.Begin{
		State: state, Provider: "corp", Nonce: strings.Repeat("n", 43), Verifier: strings.Repeat("v", 43),
		ReturnPath: returnPath,
	}
}

func TestSessionsRequestWorksOnce(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)

	state, in := begin(t, "/console/tokens")
	if err := f.svc.BeginRequest(t.Context(), in); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := f.svc.BeginRequest(t.Context(), in); !errors.Is(err, session.ErrAlreadyExists) {
		t.Fatalf("begin with a taken state = %v, want ErrAlreadyExists", err)
	}

	r, err := f.svc.TakeRequest(t.Context(), state)
	if err != nil || r.ReturnPath != "/console/tokens" || r.Provider != "corp" {
		t.Fatalf("take = %+v, %v", r, err)
	}

	if _, err := f.svc.TakeRequest(t.Context(), state); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("second take = %v, want ErrNotFound", err)
	}
}

func TestSessionsRequestExpires(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)

	state, in := begin(t, "/")
	if err := f.svc.BeginRequest(t.Context(), in); err != nil {
		t.Fatalf("begin: %v", err)
	}

	f.clock.Add(session.RequestLifetime)

	if _, err := f.svc.TakeRequest(t.Context(), state); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("take after the lifetime = %v, want ErrNotFound", err)
	}
}

func TestSessionsRequestRefusesReturnPath(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)

	for _, path := range []string{"https://evil.example/", "//evil.example/", "relative"} {
		_, in := begin(t, path)
		if err := f.svc.BeginRequest(t.Context(), in); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("begin with return path %q = %v, want ErrInvalid", path, err)
		}
	}
}

func TestNewSessionsNeedsDependencies(t *testing.T) {
	t.Parallel()

	pol, err := session.NewPolicies(
		session.PolicyConfig{Store: &memoryPolicies{policy: session.DefaultPolicy(), entries: nil}, Now: nil},
	)
	if err != nil {
		t.Fatalf("policies: %v", err)
	}

	for _, cfg := range []session.SessionsConfig{
		{Store: nil, Policies: pol, Now: nil},
		{Store: disabledOwner{Store: nil}, Policies: nil, Now: nil},
	} {
		if _, err := session.NewSessions(cfg); !errors.Is(err, session.ErrMissingDependency) {
			t.Errorf("NewSessions(%+v) = %v, want ErrMissingDependency", cfg, err)
		}
	}
}
