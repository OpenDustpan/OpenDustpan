package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	store *sqlite.Store
	svc   *session.Sessions
	clock *clock
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

	svc, err := session.NewSessions(session.SessionsConfig{
		Store: st, Providers: nil, Inactivity: nil, Now: c.Now,
	})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	return sessionsFixture{store: st, svc: svc, clock: c}
}

// signInEntry is the entry of the caller of a sign-in.
func signInEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "corp:alice", IDPSubject: "alice", TokenID: "",
		Action: session.ActionSignIn, Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: "", Outcome: "",
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0, ObjectID: "", FenceVersion: 0,
	}
}

func startInput(authTime time.Time) session.Start {
	return session.Start{
		AuthTime: authTime, PrincipalID: personID, Provider: "corp", SourceIP: "192.0.2.1", UserAgent: "test",
		Stored: false,
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

	f.setLimits(t, session.Limits{Idle: session.MinIdle, MaxLength: time.Hour})
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

// countingTouches is a session store that counts the TouchSession calls.
type countingTouches struct {
	*sqlite.Store

	touches atomic.Int64
}

func (c *countingTouches) TouchSession(
	ctx context.Context, id string, at time.Time, every time.Duration, l session.Limits,
) error {
	c.touches.Add(1)

	return c.Store.TouchSession(ctx, id, at, every, l)
}

// TestSessionsSkipRecentTouch proves that a session used inside TouchEvery
// of its last use sends no touch to the store, and that a later use does.
func TestSessionsSkipRecentTouch(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	counting := &countingTouches{Store: f.store, touches: atomic.Int64{}}

	svc, err := session.NewSessions(session.SessionsConfig{
		Store: counting, Providers: nil, Inactivity: nil, Now: f.clock.Now,
	})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	_, secret, err := svc.Start(t.Context(), signInEntry(), startInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	f.clock.Add(30 * time.Second)

	if _, err := svc.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if n := counting.touches.Load(); n != 0 {
		t.Fatalf("touches 30 seconds after the last use = %d, want 0", n)
	}

	f.clock.Add(time.Minute)

	if _, err := svc.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if n := counting.touches.Load(); n != 1 {
		t.Fatalf("touches 90 seconds after the last use = %d, want 1", n)
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

	svc, err := session.NewSessions(
		session.SessionsConfig{
			Store: disabledOwner{f.store}, Providers: nil, Inactivity: nil, Now: f.clock.Now,
		},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	if _, err := svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate with a disabled owner = %v, want ErrUnauthenticated", err)
	}
}

// fixedInactivity answers a fixed result for every owner, and keeps the
// owner and the policy of the last call.
type fixedInactivity struct {
	inactive bool
	mu       sync.Mutex
	last     session.Owner
	policy   session.AccountPolicy
}

func (f *fixedInactivity) AccountInactive(o session.Owner, p session.AccountPolicy, _ time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.last, f.policy = o, p

	return f.inactive
}

// withInactivity returns the service of f with the inactivity rule rule.
func (f sessionsFixture) withInactivity(t *testing.T, rule session.InactivityRule) *session.Sessions {
	t.Helper()

	svc, err := session.NewSessions(
		session.SessionsConfig{Store: f.store, Providers: nil, Inactivity: rule, Now: f.clock.Now},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	return svc
}

// lastSeen returns the last use of the session id in the store of f.
func (f sessionsFixture) lastSeen(t *testing.T, id string) time.Time {
	t.Helper()

	got, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	return got.LastSeenAt
}

func TestSessionsRefuseInactiveAccount(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	sess, secret := f.start(t)

	f.clock.Add(5 * time.Minute)

	var (
		nobody   session.Owner
		noPolicy session.AccountPolicy
	)

	rule := &fixedInactivity{inactive: true, mu: sync.Mutex{}, last: nobody, policy: noPolicy}
	svc := f.withInactivity(t, rule)

	if _, err := svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate with an inactive account = %v, want ErrUnauthenticated", err)
	}

	if rule.last.ID != personID || rule.last.ActiveAt.IsZero() {
		t.Fatalf("rule saw owner %+v, want %s with its activity time", rule.last, personID)
	}

	stored, err := f.store.GetPolicy(t.Context())
	if err != nil {
		t.Fatalf("get access token policy: %v", err)
	}

	if rule.policy.DisableAccounts != stored.DisableAccounts || rule.policy.Inactivity != stored.Inactivity {
		t.Fatalf("rule saw policy %+v, want the stored account policy %+v", rule.policy, stored)
	}

	if got := f.lastSeen(t, sess.ID); !got.Equal(sess.LastSeenAt) {
		t.Fatalf("last use after a refused request = %s, want %s", got, sess.LastSeenAt)
	}

	rule.mu.Lock()
	rule.inactive = false
	rule.mu.Unlock()

	if _, err := svc.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate with an active account: %v", err)
	}
}

// TestSessionsUseNearTheIdleLimitMovesIt proves that the touch skip never
// lets a session in use go idle: a use 30 seconds before the idle limit is
// recorded, so a use 30 seconds after the first limit still succeeds.
func TestSessionsUseNearTheIdleLimitMovesIt(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	sess, secret := f.start(t)

	f.clock.Add(session.DefaultIdle - 30*time.Second)

	if _, err := f.svc.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate 30 seconds before the idle limit: %v", err)
	}

	if got := f.lastSeen(t, sess.ID); !got.Equal(f.svc.Now()) {
		t.Fatalf("last use after the use before the idle limit = %s, want %s", got, f.svc.Now())
	}

	f.clock.Add(time.Minute)

	if _, err := f.svc.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate 30 seconds after the first idle limit: %v", err)
	}

	if got := f.lastSeen(t, sess.ID); !got.Equal(f.svc.Now()) {
		t.Fatalf("last use after the second use = %s, want %s", got, f.svc.Now())
	}
}

// setLimits stores the session limits l in the store of f.
func (f sessionsFixture) setLimits(t *testing.T, l session.Limits) {
	t.Helper()

	_, err := f.store.UpdateSessionPolicy(t.Context(), func(old session.Policy) (session.Policy, audit.Entry, error) {
		old.Limits, old.UpdatedAt = l, f.clock.Now()

		e := signInEntry()
		e.Action, e.At, e.Namespace, e.Kind, e.Outcome, e.Object = "server:manage-sessions", f.clock.Now(),
			audit.ServerScope, audit.KindChange, audit.OutcomeSuccess, session.PolicyRef

		return old, e, nil
	})
	if err != nil {
		t.Fatalf("set session limits: %v", err)
	}
}

// TestSessionsRefusedKeepsLastUse proves that a request that an idle or
// expired session refuses records no use.
func TestSessionsRefusedKeepsLastUse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		advance time.Duration
	}{
		{name: "idle", advance: session.DefaultIdle},
		{name: "expired", advance: session.DefaultMaxLength},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newSessionsFixture(t)
			sess, secret := f.start(t)

			f.clock.Add(tc.advance)

			if _, err := f.svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
				t.Fatalf("authenticate = %v, want ErrUnauthenticated", err)
			}

			if got := f.lastSeen(t, sess.ID); !got.Equal(sess.LastSeenAt) {
				t.Fatalf("last use after a refused request = %s, want %s", got, sess.LastSeenAt)
			}
		})
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
		ReturnPath: returnPath, MaxAge: 0, HasMaxAge: false,
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

func TestSessionsPruneRequests(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)

	_, old := begin(t, "/")
	if err := f.svc.BeginRequest(t.Context(), old); err != nil {
		t.Fatalf("begin: %v", err)
	}

	f.clock.Add(session.RequestLifetime / 2)

	fresh, in := begin(t, "/")
	if err := f.svc.BeginRequest(t.Context(), in); err != nil {
		t.Fatalf("begin: %v", err)
	}

	f.clock.Add(session.RequestLifetime / 2)

	n, err := f.svc.PruneRequests(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v; want 1 expired request", n, err)
	}

	if _, err := f.svc.TakeRequest(t.Context(), fresh); err != nil {
		t.Fatalf("take of the request that did not expire: %v", err)
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

	cfg := session.SessionsConfig{Store: nil, Providers: nil, Inactivity: nil, Now: nil}
	if _, err := session.NewSessions(cfg); !errors.Is(err, session.ErrMissingDependency) {
		t.Errorf("NewSessions(%+v) = %v, want ErrMissingDependency", cfg, err)
	}
}

// TestStartStoredNeedsProviderSessions proves that the start of a session
// of a stored provider fails, and creates no session, when the service has
// no provider session store.
func TestStartStoredNeedsProviderSessions(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	in := startInput(f.clock.Now())
	in.Stored = true

	if _, _, err := f.svc.Start(t.Context(), signInEntry(), in); !errors.Is(err, session.ErrMissingDependency) {
		t.Fatalf("stored start with no provider session store = %v, want ErrMissingDependency", err)
	}
}
