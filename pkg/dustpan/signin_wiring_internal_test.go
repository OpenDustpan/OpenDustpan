package dustpan

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// lockedLog is a log buffer that the server and the test share.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// wiredSignIn is a server from NewServer with the console sign-in, its
// store, its clock and its log.
type wiredSignIn struct {
	srv   *Server
	store *sqlite.Store
	clock *sweepClock
	log   *lockedLog
}

// newWiredSignIn builds a server through NewServer with one OpenID Connect
// provider. No test here signs in through the provider: the sessions come
// from the store, so the provider is never read. The options that extra
// returns for the store run after the options of the fixture, so they can
// replace one of them. A nil extra adds no option.
func newWiredSignIn(t *testing.T, extra func(st *sqlite.Store) []Option) wiredSignIn {
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

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	authn, err := identity.NewStaticToken("admin", "admin-token-for-the-sign-in-wiring-test-of-the-server")
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	//nolint:exhaustruct_v5 // The test listens with defaults; only the context matters.
	lc := &net.ListenConfig{}

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	clock := &sweepClock{mu: sync.Mutex{}, now: time.Now().UTC().Truncate(time.Second)}
	log := &lockedLog{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	opts := []Option{
		WithInstallation("test"), WithNamespaceStore(st), WithStackStore(st), WithUpdateStore(st),
		WithCheckpointStore(st), WithEventStore(st), WithSecretKeyStore(st), WithSecretWrapper(wrapper),
		WithWorkspaceStore(st), WithStateStore(st), WithBlobStore(blobs), WithInventoryReader(st),
		WithAuthenticator(authn), WithListener(ln), WithClock(clock.Now),
		WithLogger(slog.New(slog.NewTextHandler(log, nil))), WithPublicURL("https://dustpan.example"),
		WithOIDCProvider(OIDCProvider{
			HTTPClient: nil, ID: "corp", DisplayName: "Corp", Issuer: "https://idp.example", ClientID: "dustpan",
			ClientSecret: "client-secret-for-tests", Scopes: nil, AutoCreate: true,
		}),
	}

	if extra != nil {
		opts = append(opts, extra(st)...)
	}

	srv, err := NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return wiredSignIn{srv: srv, store: st, clock: clock, log: log}
}

// person creates the person id, made at the time of the clock, with the
// break-glass flag breakGlass.
func (w wiredSignIn) person(t *testing.T, id string, breakGlass bool) {
	t.Helper()

	now := w.clock.Now()
	p := principal.Principal{
		CreatedAt: now, UpdatedAt: now, DisabledAt: time.Time{}, ID: id, Subject: "corp:" + id,
		DisplayName: id, Kind: principal.KindPerson, Source: principal.SourceOIDC, Namespace: "", GroupCount: 0,
		BreakGlass: breakGlass,
	}

	if err := w.store.CreatePrincipal(t.Context(), p, wiringEntry(now, principal.Ref(id))); err != nil {
		t.Fatalf("create principal %s: %v", id, err)
	}
}

// session starts a session of the person id at the time of the clock and
// returns its secret key.
func (w wiredSignIn) session(t *testing.T, id string) session.Secret {
	t.Helper()

	key, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	now := w.clock.Now()
	ss := session.Session{
		CreatedAt: now, LastSeenAt: now, AuthTime: now, EndedAt: time.Time{}, ID: "ses-" + id, PrincipalID: id,
		Provider: "corp", EndReason: "", SourceIP: "192.0.2.1", UserAgent: "test",
		Generation: 1,
	}

	err = w.store.CreateSession(t.Context(), ss, session.DigestOf(key), wiringEntry(now, "ses-"+id))
	if err != nil {
		t.Fatalf("create session of %s: %v", id, err)
	}

	return key
}

// readSession answers GET /api/server/session with the session cookie of
// key.
func (w wiredSignIn) readSession(t *testing.T, key session.Secret) int {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/session", http.NoBody)
	req.AddCookie(&http.Cookie{
		Name: "__Host-dustpan_session", Value: key.Reveal(), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	if err := w.srv.Start(t.Context()); err != nil {
		t.Fatalf("start the server: %v", err)
	}

	rec := httptest.NewRecorder()
	w.srv.Handler().ServeHTTP(rec, req)

	return rec.Code
}

func wiringEntry(at time.Time, object string) audit.Entry {
	return audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "", Action: "test",
		Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// TestNewServerSessionsApplyInactivity proves that NewServer wires the
// account inactivity rule of the token service into the console sessions:
// a session of a person past the inactivity period is refused, and a
// session of a break-glass person is not.
func TestNewServerSessionsApplyInactivity(t *testing.T) {
	t.Parallel()

	w := newWiredSignIn(t, nil)
	ctx := t.Context()

	inactivity, on := time.Hour, true

	change := accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: &inactivity, DisableTokens: nil, DisableAccounts: &on, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	}

	if _, err := w.srv.tokens.SetPolicy(ctx, wiringEntry(w.clock.Now(), accesstoken.PolicyRef), change); err != nil {
		t.Fatalf("set the token policy: %v", err)
	}

	// Sessions that live longer than the inactivity period, so only the
	// account rule can end them.
	_, err := w.store.UpdateSessionPolicy(ctx, func(old session.Policy) (session.Policy, audit.Entry, error) {
		old.Limits = session.Limits{Idle: session.MaxLengthBound, MaxLength: session.MaxLengthBound}
		old.UpdatedAt = w.clock.Now()

		return old, wiringEntry(w.clock.Now(), session.PolicyRef), nil
	})
	if err != nil {
		t.Fatalf("set the session policy: %v", err)
	}

	w.person(t, "prn-alice", false)
	w.person(t, "prn-glass", true)

	alice := w.session(t, "prn-alice")
	glass := w.session(t, "prn-glass")

	if code := w.readSession(t, alice); code != http.StatusOK {
		t.Fatalf("session of an active person = %d, want 200", code)
	}

	w.clock.advance(2 * time.Hour)

	if code := w.readSession(t, alice); code != http.StatusUnauthorized {
		t.Fatalf("session of a person past the inactivity period = %d, want 401", code)
	}

	if code := w.readSession(t, glass); code != http.StatusOK {
		t.Fatalf("session of a break-glass person past the inactivity period = %d, want 200", code)
	}
}

// TestRunPrunesSignInRequests proves that the run loop of a server from
// NewServer deletes the expired sign-in requests.
func TestRunPrunesSignInRequests(t *testing.T) {
	t.Parallel()

	w := newWiredSignIn(t, nil)
	w.srv.signInPruneEvery = 20 * time.Millisecond

	state, err := session.NewSecret()
	if err != nil {
		t.Fatalf("state: %v", err)
	}

	now := w.clock.Now()
	r := session.Request{
		CreatedAt: now, ExpiresAt: now.Add(session.RequestLifetime), Provider: "corp",
		Nonce: "n-0S6_WzA2Mj-rG7k3QpL", Verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", ReturnPath: "/",
		MaxAge: 0, HasMaxAge: false,
	}

	if err := w.store.CreateRequest(t.Context(), r, session.DigestOf(state)); err != nil {
		t.Fatalf("create request: %v", err)
	}

	w.clock.advance(session.RequestLifetime)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- w.srv.Run(ctx) }()

	wait, stopWait := context.WithTimeout(t.Context(), 20*time.Second)
	defer stopWait()

	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()

	for !strings.Contains(w.log.String(), "pruned the expired sign-in requests") && wait.Err() == nil {
		select {
		case <-wait.Done():
		case <-poll.C:
		}
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(w.log.String(), "requests=1") {
		t.Fatalf("log = %q, want a prune of one expired request", w.log.String())
	}
}
