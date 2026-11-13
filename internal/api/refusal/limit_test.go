package refusal_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// person returns an authenticated principal with the subject.
func person(subject string) identity.Principal {
	return identity.Principal{
		Subject: subject, TokenID: "tok-1", DisplayName: "", Admin: false, TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}
}

func newRecorder(
	t *testing.T, store audit.Store, lim *denylimit.Limiter, failures *int,
) *refusal.Recorder {
	t.Helper()

	rec, err := refusal.New(fakeAuthorizer{err: identity.ErrDenied}, store, fakeNamespaces{},
		slog.New(slog.DiscardHandler), func() { *failures++ })
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return rec.WithLimiter(lim)
}

func request(t *testing.T, path string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, path+"?token=hunter2",
		strings.NewReader(`{"secret":"hunter2"}`))
	req.Header.Set("Authorization", "Bearer hunter2")

	return req
}

// TestEntryHoldsNoSecret checks that the entry names the route and the reason,
// and holds no query value, header value or body.
func TestEntryHoldsNoSecret(t *testing.T) {
	t.Parallel()

	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, nil, &failures)

	deny(t, rec, request(t, "/api/inventory/unit-lock"), person("bo@example.com"),
		identity.NamespaceObject("team"))

	if len(store.entries) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(store.entries))
	}

	e := store.entries[0]

	if want := "denied by policy on PUT /api/inventory/unit-lock"; e.Reason != want {
		t.Fatalf("reason = %q, want %q", e.Reason, want)
	}

	if err := e.Validate(); err != nil {
		t.Fatalf("entry is not valid: %v", err)
	}

	if text := fmt.Sprintf("%+v", e); strings.Contains(text, "hunter2") {
		t.Fatalf("entry holds a secret of the request: %s", text)
	}
}

// TestHostileTextIsCleaned sends control characters, a line break, a bidi
// override and a very long subject straight to the recorder. The entry must
// stay valid, so that the append does not fail, and hold none of them.
func TestHostileTextIsCleaned(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		subject string
	}{
		{"line break", "bo\r\nFAKE entry"},
		{"nul", "bo\x00admin"},
		{"bidi override", "bo\u202eadmin"},
		{"escape", "bo\x1b[2Jadmin"},
		{"invalid utf-8", "bo\xff\xfeadmin"},
		{"long", strings.Repeat("x", 10000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store, failures := newFakeStore(nil), 0
			rec := newRecorder(t, store, nil, &failures)

			req := request(t, "/api/x%0d%0ay")
			req.Header.Set("User-Agent", tc.subject)

			deny(t, rec, req, person(tc.subject),
				identity.NamespaceObject("team"))

			if len(store.entries) != 1 || failures != 0 {
				t.Fatalf("wrote %d entries and counted %d failures, want 1 and 0", len(store.entries), failures)
			}

			e := store.entries[0]

			checkClean(t, e)

			if err := e.Validate(); err != nil {
				t.Fatalf("entry is not valid: %v", err)
			}
		})
	}
}

// TestFloodWritesOneEntryAndCountsTheRest checks the flood limit: 1000
// refusals of one principal in one window write one entry, and the count goes
// out with the first entry of the next window.
func TestFloodWritesOneEntryAndCountsTheRest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(
		denylimit.Config{
			Window:              time.Minute,
			MaxKeys:             0,
			MaxFlush:            0,
			Now:                 func() time.Time { return now },
			MaxKeysPerPrincipal: 0,
		},
	)
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	send := func(subject string) {
		deny(t, rec, request(t, "/api/x"), person(subject),
			identity.NamespaceObject("team"))
	}

	for range 1000 {
		send("bo@example.com")
	}

	send("ann@example.com")

	if len(store.entries) != 2 {
		t.Fatalf("wrote %d entries, want 1 for bo and 1 for ann", len(store.entries))
	}

	now = now.Add(time.Minute)

	send("bo@example.com")

	last := store.entries[len(store.entries)-1]
	if !strings.Contains(last.Reason, "999 further refusals") {
		t.Fatalf("reason of the next window = %q, want the count 999", last.Reason)
	}
}

// TestQuietPrincipalIsFlushed checks that the count of a principal that stops
// is written by the next refusal of anyone, after the window.
func TestQuietPrincipalIsFlushed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(
		denylimit.Config{
			Window:              time.Minute,
			MaxKeys:             0,
			MaxFlush:            0,
			Now:                 func() time.Time { return now },
			MaxKeysPerPrincipal: 0,
		},
	)
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	for range 6 {
		deny(t, rec, request(t, "/api/x"), person("bo@example.com"),
			identity.NamespaceObject("team"))
	}

	now = now.Add(3 * time.Minute)

	deny(t, rec, request(t, "/api/x"), person("ann@example.com"),
		identity.NamespaceObject("team"))

	var flushed *audit.Entry

	for i := range store.entries {
		if store.entries[i].Subject == "bo@example.com" && strings.Contains(store.entries[i].Reason, "5 further") {
			flushed = &store.entries[i]
		}
	}

	if flushed == nil {
		t.Fatalf("no flush entry for bo in %+v", store.entries)
	}
}

// TestOverflowWritesOneAggregate checks the overflow path: with 4 tracked keys
// and 500 principals, memory stays at 4 keys and the rest of the refusals
// produce one aggregate entry in the installation scope.
func TestOverflowWritesOneAggregate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(
		denylimit.Config{
			Window:              time.Minute,
			MaxKeys:             4,
			MaxFlush:            0,
			Now:                 func() time.Time { return now },
			MaxKeysPerPrincipal: 0,
		},
	)
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	for i := range 500 {
		deny(t, rec, request(t, "/api/x"), person(fmt.Sprintf("p%d@example.com", i)),
			identity.NamespaceObject("team"))
	}

	if lim.Len() != 4 {
		t.Fatalf("tracked keys = %d, want 4", lim.Len())
	}

	aggregates := 0

	for _, e := range store.entries {
		if e.Action == "denials:suppressed" {
			aggregates++

			if e.Namespace != audit.ServerScope {
				t.Fatalf("aggregate scope = %q, want the installation scope", e.Namespace)
			}
		}
	}

	if aggregates != 1 || len(store.entries) != 5 {
		t.Fatalf("wrote %d entries with %d aggregates, want 5 and 1", len(store.entries), aggregates)
	}
}

// TestRecordWritesADomainRefusal checks the entry of a refusal that a domain
// package decides, with its own reason.
func TestRecordWritesADomainRefusal(t *testing.T) {
	t.Parallel()

	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, nil, &failures)

	rec.Record(t.Context(), request(t, "/api/inventory/unit-lock"), person("bo@example.com"), refusal.Denial{
		Action: identity.ActionUnitManage, Object: identity.NamespaceObject("team"),
		Reason: "not the holder of the lock\r\n", ReasonWithin: nil, Window: 0,
	})

	if len(store.entries) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(store.entries))
	}

	e := store.entries[0]
	if e.Outcome != audit.OutcomeDenied || !strings.HasPrefix(e.Reason, "not the holder of the lock") ||
		strings.ContainsAny(e.Reason, "\r\n") {
		t.Fatalf("entry = %+v, want a denied entry with the cleaned reason", e)
	}
}

// TestFailedAppendKeepsTheRefusalUnderFlood checks that a failing store never
// changes the answer and that every failed append is counted.
func TestFailedAppendKeepsTheRefusalUnderFlood(t *testing.T) {
	t.Parallel()

	store, failures := newFakeStore(errDiskFull), 0
	rec := newRecorder(t, store, nil, &failures)

	for range 3 {
		err := rec.Authorize(t.Context(), request(t, "/api/x"), person("bo@example.com"), identity.ActionUnitWrite,
			identity.NamespaceObject("team"))
		if err == nil {
			t.Fatal("a failed audit append turned a refusal into a success")
		}
	}

	if failures != 1 {
		t.Fatalf("counted %d failures, want 1 (the limiter holds back the other 2)", failures)
	}
}

// deny sends one refused request through rec and fails the test if the answer
// is not a refusal.
func deny(
	t *testing.T, rec *refusal.Recorder, req *http.Request, p identity.Principal, obj identity.Object,
) {
	t.Helper()

	if err := rec.Authorize(t.Context(), req, p, identity.ActionUnitWrite, obj); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize answers %v, want %v", err, identity.ErrDenied)
	}
}

// checkClean fails the test if e holds a control character or a subject
// longer than the bound.
func checkClean(t *testing.T, e audit.Entry) {
	t.Helper()

	for _, text := range []string{e.Subject, e.Object, e.Reason, e.UserAgent, e.TokenID} {
		if strings.ContainsAny(text, "\r\n\x00\x1b\u202e") || strings.Contains(text, "\xff") {
			t.Fatalf("entry text %q holds a control character", text)
		}
	}

	if len(e.Subject) > 255 {
		t.Fatalf("subject has %d bytes, want at most 255", len(e.Subject))
	}
}

// everyNamespace knows every namespace that it is asked for.
type everyNamespace struct{}

func (everyNamespace) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		CreatedAt: time.Time{}, ID: "", Name: name, PrimaryInstallation: "", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// TestFloodOfNamespacesDoesNotHideAnotherPrincipal checks the bound on keys: one
// principal that names many namespaces holds at most MaxKeysPerPrincipal keys
// and one aggregate key, so that a second principal is still recorded, with its
// own name. The table stays bounded.
func TestFloodOfNamespacesDoesNotHideAnotherPrincipal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 8, MaxFlush: 0, MaxKeysPerPrincipal: 3, Now: func() time.Time { return now },
	})
	store, failures := newFakeStore(nil), 0
	built, newErr := refusal.New(fakeAuthorizer{err: identity.ErrDenied}, store, everyNamespace{},
		slog.New(slog.DiscardHandler), func() { failures++ })
	if newErr != nil {
		t.Fatalf("New: %v", newErr)
	}

	rec := built.WithLimiter(lim)

	for i := range 2000 {
		deny(t, rec, request(t, "/api/x"), person("bo@example.com"),
			identity.NamespaceObject(namespace.Name(fmt.Sprintf("ns%d", i))))
	}

	if lim.Len() > 4 {
		t.Fatalf("tracked keys = %d, want at most 3 and the aggregate key", lim.Len())
	}

	before := len(store.entries)

	deny(t, rec, request(t, "/api/x"), person("ann@example.com"), identity.NamespaceObject("ns-ann"))

	if len(store.entries) != before+1 || store.entries[before].Subject != "ann@example.com" {
		t.Fatalf("entries after ann = %+v, want one more that names ann", store.entries[before:])
	}

	for i := range store.entries[:before] {
		if store.entries[i].Subject != "bo@example.com" {
			t.Fatalf("entry %d names %q, want bo@example.com", i, store.entries[i].Subject)
		}
	}

	if failures != 0 {
		t.Fatalf("counted %d failures, want 0", failures)
	}
}

// TestUnknownNamespacesAreOneScope checks that a flood of names of namespaces
// that do not exist holds one key for each action and object kind.
func TestUnknownNamespacesAreOneScope(t *testing.T) {
	t.Parallel()

	lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	for i := range 500 {
		deny(t, rec, request(t, "/api/x"), person("bo@example.com"),
			identity.NamespaceObject(namespace.Name(fmt.Sprintf("gone%d", i))))
	}

	if lim.Len() != 1 || len(store.entries) != 1 {
		t.Fatalf("tracked keys = %d and entries = %d, want 1 and 1", lim.Len(), len(store.entries))
	}

	if e := store.entries[0]; e.Namespace != audit.ServerScope || !strings.Contains(e.Reason, "does not exist") {
		t.Fatalf("entry = %+v, want the installation scope and the missing namespace in the reason", e)
	}
}

// TestFlushWritesHeldCounts checks that Flush writes the counts that no entry
// carries and empties the limiter, so a second Flush writes nothing.
func TestFlushWritesHeldCounts(t *testing.T) {
	t.Parallel()

	lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	for range 6 {
		deny(t, rec, request(t, "/api/x"), person("bo@example.com"), identity.NamespaceObject("team"))
	}

	rec.Flush(t.Context())

	if len(store.entries) != 2 || !strings.Contains(store.entries[1].Reason, "5 further refusals") ||
		store.entries[1].Subject != "bo@example.com" {
		t.Fatalf("entries = %+v, want the first refusal and a flush entry with the count 5", store.entries)
	}

	rec.Flush(t.Context())

	if len(store.entries) != 2 || lim.Len() != 0 {
		t.Fatalf("second flush: entries = %d and keys = %d, want 2 and 0", len(store.entries), lim.Len())
	}
}

// timedStore is a fakeStore that records the order of each append and the
// deadline of its context. An append of an entry whose subject is failSubject
// fails at once, as an append does when its own timeout ends. The test does not
// wait for a real timeout.
type timedStore struct {
	*fakeStore

	failSubject string
	order       []string
	deadlines   map[string]time.Time
}

func (s *timedStore) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := s.record(ctx, e); err != nil {
		return audit.Entry{}, err
	}

	return s.fakeStore.Append(ctx, fence, e)
}

func (s *timedStore) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	if err := s.record(ctx, e); err != nil {
		return audit.Entry{}, err
	}

	return s.fakeStore.AppendServer(ctx, e)
}

func (s *timedStore) record(ctx context.Context, e audit.Entry) error {
	s.order = append(s.order, e.Subject)

	if d, ok := ctx.Deadline(); ok {
		s.deadlines[e.Subject] = d
	}

	if e.Subject == s.failSubject {
		return fmt.Errorf("append: %w", context.DeadlineExceeded)
	}

	return nil
}

// TestSlowFlushKeepsTheEntryOfTheRefusal checks that a flush entry that fails
// at its timeout cannot make a refusal lose its own entry. The entry of the
// refusal comes first, and each write has a deadline of its own.
func TestSlowFlushKeepsTheEntryOfTheRefusal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(denylimit.Config{
		Window:              time.Minute,
		MaxKeys:             0,
		MaxFlush:            0,
		Now:                 func() time.Time { return now },
		MaxKeysPerPrincipal: 0,
	})
	store := &timedStore{
		fakeStore:   newFakeStore(nil),
		failSubject: "",
		order:       nil,
		deadlines:   make(map[string]time.Time),
	}
	failures := 0
	rec := newRecorder(t, store, lim, &failures)

	for range 6 {
		deny(t, rec, request(t, "/api/x"), person("bo@example.com"), identity.NamespaceObject("team"))
	}

	now = now.Add(3 * time.Minute)
	store.failSubject = "bo@example.com"
	store.order = nil

	deny(t, rec, request(t, "/api/x"), person("ann@example.com"), identity.NamespaceObject("team"))

	want := []string{"ann@example.com", "bo@example.com"}
	if !slices.Equal(store.order, want) {
		t.Fatalf("append order = %v, want %v: the flush entry came before the entry of the refusal", store.order, want)
	}

	ann, bo := store.deadlines["ann@example.com"], store.deadlines["bo@example.com"]
	if ann.IsZero() || bo.IsZero() {
		t.Fatalf("an append had no deadline: ann %v, bo %v", ann, bo)
	}

	if !bo.After(ann) {
		t.Fatalf("flush deadline %v is not after the deadline of the entry %v: both writes share one budget", bo, ann)
	}

	if gap := bo.Sub(ann); gap > time.Second {
		t.Fatalf("flush deadline %v is %v after the deadline of the entry %v, "+
			"want at most about 1s: the flush has a timeout of its own", bo, gap, ann)
	}

	if rest := time.Until(ann); rest < 4*time.Second {
		t.Fatalf("the entry of the refusal has %v left of its write timeout, want about 5s", rest)
	}

	if failures != 1 {
		t.Fatalf("counted %d failures, want 1 (the failed flush entry of bo)", failures)
	}

	if len(store.entries) != 2 || store.entries[len(store.entries)-1].Subject != "ann@example.com" {
		t.Fatalf("written entries = %+v, want the entry of ann and the first refusal of bo", store.entries)
	}
}

// TestCanceledRequestStillWritesTheEntry checks that a request context that is
// done does not lose the entry of the refusal.
func TestCanceledRequestStillWritesTheEntry(t *testing.T) {
	t.Parallel()

	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, nil, &failures)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := rec.Authorize(ctx, request(t, "/api/x"), person("bo@example.com"), identity.ActionUnitWrite,
		identity.NamespaceObject("team"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize answers %v, want %v", err, identity.ErrDenied)
	}

	if len(store.entries) != 1 || failures != 0 {
		t.Fatalf("wrote %d entries and counted %d failures, want 1 and 0", len(store.entries), failures)
	}
}

// TestAuthorizerFailureIsRecordedWithoutItsText checks that an error of the
// Authorizer that is not a refusal writes an entry with a fixed reason, and the
// error text stays out of it.
func TestAuthorizerFailureIsRecordedWithoutItsText(t *testing.T) {
	t.Parallel()

	store, failures := newFakeStore(nil), 0
	built, newErr := refusal.New(fakeAuthorizer{err: errStoreDown}, store, fakeNamespaces{},
		slog.New(slog.DiscardHandler), func() { failures++ })
	if newErr != nil {
		t.Fatalf("New: %v", newErr)
	}

	rec := built

	err := rec.Authorize(t.Context(), request(t, "/api/x"), person("bo@example.com"), identity.ActionUnitWrite,
		identity.NamespaceObject("team"))
	if !errors.Is(err, errStoreDown) {
		t.Fatalf("Authorize answers %v, want %v", err, errStoreDown)
	}

	if len(store.entries) != 1 || !strings.HasPrefix(store.entries[0].Reason, refusal.ReasonFailed) ||
		strings.Contains(store.entries[0].Reason, "store down") {
		t.Fatalf(
			"entries = %+v, want one entry with the reason %q and no error text",
			store.entries,
			refusal.ReasonFailed,
		)
	}
}

// TestRecordWithNoRequest checks that a caller with no request gets a valid
// entry with no address and no user agent.
func TestRecordWithNoRequest(t *testing.T) {
	t.Parallel()

	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, nil, &failures)

	rec.Record(t.Context(), nil, person("bo@example.com"), refusal.Denial{
		Action:       identity.ActionUnitManage,
		Object:       identity.NamespaceObject("team"),
		Reason:       "not the holder",
		ReasonWithin: nil,
		Window:       0,
	})

	if len(store.entries) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(store.entries))
	}

	e := store.entries[0]
	if err := e.Validate(); err != nil || e.SourceIP != "" || e.UserAgent != "" {
		t.Fatalf("entry = %+v, validate = %v, want a valid entry with no address and no user agent", e, err)
	}
}

// TestToolsAreSeparateKeys checks that one principal, action and namespace
// write one entry for each tool: a refusal for an OpenTofu workspace must not
// hide a refusal for a Pulumi stack in the same window.
func TestToolsAreSeparateKeys(t *testing.T) {
	t.Parallel()

	lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	for _, tool := range []string{"opentofu", "pulumi"} {
		obj, err := identity.UnitObject(tool, "team", "net")
		if err != nil {
			t.Fatalf("unit object %s: %v", tool, err)
		}

		for range 3 {
			deny(t, rec, request(t, "/api/x"), person("bo@example.com"), obj)
		}
	}

	if lim.Len() != 2 || len(store.entries) != 2 {
		t.Fatalf("tracked keys = %d and entries = %d, want 2 and 2", lim.Len(), len(store.entries))
	}
}

// brokenNamespaces fails every read with an error that is not ErrNotFound.
type brokenNamespaces struct{}

var errBrokenNamespaces = errors.New("namespace store is down")

func (brokenNamespaces) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, errBrokenNamespaces
}

// TestRawScopeIsCleanedInTheKey checks that a namespace name with control
// characters, read while the namespace store fails, reaches the key of the
// limiter cleaned and cut to the bound.
func TestRawScopeIsCleanedInTheKey(t *testing.T) {
	t.Parallel()

	lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
	failures := 0
	built, newErr := refusal.New(fakeAuthorizer{err: identity.ErrDenied}, newFakeStore(nil), brokenNamespaces{},
		slog.New(slog.DiscardHandler), func() { failures++ })
	if newErr != nil {
		t.Fatalf("New: %v", newErr)
	}

	rec := built.WithLimiter(lim)

	hostile := namespace.Name("a\r\nb\x1b\u202e" + strings.Repeat("x", 600))

	for range 2 {
		deny(t, rec, request(t, "/api/x"), person("bo@example.com"), identity.NamespaceObject(hostile))
	}

	pending := lim.Drain()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want one key", pending)
	}

	scope := pending[0].Key.Scope
	if strings.ContainsAny(scope, "\r\n\x1b\u202e") || len(scope) > 255 {
		t.Fatalf("scope in the key = %q (%d bytes), want it cleaned and at most 255 bytes", scope, len(scope))
	}
}

// TestSurfacesAreSeparateKeys checks that two adapters that share a limiter
// write one entry each for the same principal, action and unit in one window.
func TestSurfacesAreSeparateKeys(t *testing.T) {
	t.Parallel()

	lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
	store, failures := newFakeStore(nil), 0

	obj, err := identity.UnitObject("opentofu", "team", "net")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	for _, surface := range []string{"tfe", "tofu-http"} {
		rec := newRecorder(t, store, lim, &failures).WithSurface(surface)

		for range 3 {
			deny(t, rec, request(t, "/api/x"), person("bo@example.com"), obj)
		}
	}

	if lim.Len() != 2 || len(store.entries) != 2 {
		t.Fatalf("tracked keys = %d and entries = %d, want 2 and 2", lim.Len(), len(store.entries))
	}
}

// TestChannelOfTheRequest checks the channel of a refusal entry: a request
// that a session signed in is the console whatever its header says, a token
// request keeps its declared channel, and an unknown or missing header and
// no request at all are the API.
func TestChannelOfTheRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		session bool
		header  string
		want    audit.Channel
	}{
		{"session without a header", true, "", audit.ChannelConsole},
		{"session that declares api", true, "api", audit.ChannelConsole},
		{"token that declares the console", false, "console", audit.ChannelConsole},
		{"token without a header", false, "", audit.ChannelAPI},
		{"token with a hostile header", false, "console\r\nX-Injected: 1", audit.ChannelAPI},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store, failures := newFakeStore(nil), 0
			rec := newRecorder(t, store, nil, &failures)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
			if tc.session {
				req = req.WithContext(sessionauth.WithSession(req.Context()))
			}

			if tc.header != "" {
				req.Header["Dustpan-Channel"] = []string{tc.header}
			}

			rec.Record(t.Context(), req, person(fmt.Sprintf("p%d@example.com", i)), refusal.Denial{
				Action: identity.ActionUnitManage, Object: identity.NamespaceObject("team"), Reason: "no",
				ReasonWithin: nil, Window: 0,
			})

			if len(store.entries) != 1 || store.entries[0].Channel != tc.want {
				t.Fatalf("entries = %+v, want one with the channel %q", store.entries, tc.want)
			}
		})
	}
}

// TestUnknownPrincipalKindIsRecordedAsNoKind checks that a refusal of a
// principal of a kind that the audit log does not know still writes a valid
// entry, with no kind.
func TestUnknownPrincipalKindIsRecordedAsNoKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind identity.Kind
		want string
	}{
		{kind: "robot", want: ""},
		{kind: "Person", want: ""},
		{kind: "person\n", want: ""},
		{kind: "", want: ""},
		{kind: identity.KindPerson, want: audit.PrincipalKindPerson},
		{kind: identity.KindService, want: audit.PrincipalKindService},
		{kind: identity.KindWorkload, want: audit.PrincipalKindWorkload},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.kind), func(t *testing.T) {
			t.Parallel()

			store, failures := newFakeStore(nil), 0
			rec := newRecorder(t, store, nil, &failures)

			p := person("bo@example.com")
			p.Kind = tc.kind

			deny(t, rec, request(t, "/api/x"), p, identity.NamespaceObject("team"))

			if len(store.entries) != 1 || failures != 0 {
				t.Fatalf("wrote %d entries and counted %d failures, want 1 and 0", len(store.entries), failures)
			}

			e := store.entries[0]
			if e.PrincipalKind != tc.want {
				t.Fatalf("principal kind = %q, want %q", e.PrincipalKind, tc.want)
			}

			if err := e.Validate(); err != nil {
				t.Fatalf("the denied entry is not valid: %v", err)
			}
		})
	}
}
