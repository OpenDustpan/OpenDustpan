package refusal_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
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

	return refusal.New(fakeAuthorizer{err: identity.ErrDenied}, store, fakeNamespaces{},
		slog.New(slog.DiscardHandler), func() { *failures++ }).WithLimiter(lim)
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
		denylimit.Config{Window: time.Minute, MaxKeys: 0, MaxFlush: 0, Now: func() time.Time { return now }},
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
		denylimit.Config{Window: time.Minute, MaxKeys: 0, MaxFlush: 0, Now: func() time.Time { return now }},
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
		denylimit.Config{Window: time.Minute, MaxKeys: 4, MaxFlush: 0, Now: func() time.Time { return now }},
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
		Reason: "not the holder of the lock\r\n",
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
