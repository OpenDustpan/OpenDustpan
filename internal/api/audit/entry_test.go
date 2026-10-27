package audit_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// getJSON serves target with the handler and decodes the body into out when
// the status is 200. It returns the status.
func getJSON(t *testing.T, handler http.Handler, target string, out any) int {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, target))

	if rec.Code == http.StatusOK && out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v (%s)", target, err, rec.Body.String())
		}
	}

	return rec.Code
}

// TestReadOneEntryBySequence proves that GET /api/audit/entries/{sequence}
// answers the one entry of the scope at that sequence, in the shape of an
// entry of the list.
func TestReadOneEntryBySequence(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})

	for _, subject := range []string{"alice", "bob", "carol"} {
		seedEntry(t, store, "acme", subject)
	}

	var list struct {
		Entries []map[string]any `json:"entries"`
	}

	if status := getJSON(t, handler, "/api/audit/entries?organization=acme", &list); status != http.StatusOK {
		t.Fatalf("list answers %d", status)
	}

	var want map[string]any

	for _, e := range list.Entries {
		if e["sequence"] == float64(2) {
			want = e
		}
	}

	var got map[string]any

	if status := getJSON(t, handler, "/api/audit/entries/2?organization=acme", &got); status != http.StatusOK {
		t.Fatalf("GET entry 2 answers %d, want %d", status, http.StatusOK)
	}

	if want == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("entry 2 = %v, want the list entry %v", got, want)
	}
}

// TestReadOneEntryErrors proves that an unknown sequence answers 404, and a
// sequence that is not a positive number in its one decimal form answers
// 400, so each entry has one URL.
func TestReadOneEntryErrors(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")

	for _, tc := range []struct {
		target string
		want   int
	}{
		{target: "/api/audit/entries/99?organization=acme", want: http.StatusNotFound},
		{target: "/api/audit/entries/0?organization=acme", want: http.StatusBadRequest},
		{target: "/api/audit/entries/-1?organization=acme", want: http.StatusBadRequest},
		{target: "/api/audit/entries/one?organization=acme", want: http.StatusBadRequest},
		{target: "/api/audit/entries/+1?organization=acme", want: http.StatusBadRequest},
		{target: "/api/audit/entries/01?organization=acme", want: http.StatusBadRequest},
	} {
		if status := getJSON(t, handler, tc.target, nil); status != tc.want {
			t.Fatalf("GET %s answers %d, want %d", tc.target, status, tc.want)
		}
	}
}

// TestReadOneServerEntry proves that the route reads the scope @server
// with server:read-audit on the installation.
func TestReadOneServerEntry(t *testing.T) {
	t.Parallel()

	recorder := &recordingAuthorizer{calls: nil}
	handler, store := newEnforcingHandler(t, recorder)
	seedServerEntry(t, store, "root")

	var got struct {
		Subject  string `json:"subject"`
		Sequence int64  `json:"sequence"`
	}

	if status := getJSON(t, handler, "/api/audit/entries/1?organization=@server", &got); status != http.StatusOK {
		t.Fatalf("GET @server entry 1 answers %d, want %d", status, http.StatusOK)
	}

	if got.Subject != "root" || got.Sequence != 1 {
		t.Fatalf("@server entry 1 = %+v, want subject root at sequence 1", got)
	}

	if len(recorder.calls) != 1 || recorder.calls[0].action != identity.ActionServerReadAudit ||
		recorder.calls[0].object != "@server" {
		t.Fatalf("authorized %+v, want only %s on @server", recorder.calls, identity.ActionServerReadAudit)
	}
}

// TestReadOneEntryWithoutReader proves that a store that cannot read one
// entry answers 501, not a failure.
func TestReadOneEntryWithoutReader(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		OnAuditFailure: nil,
		Namespaces:     namespaces,
		Entries:        entries,
		EntryReader:    nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     &recordingAuthorizer{calls: nil},
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		DenialLimiter:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	status := getJSON(t, handler, "/api/audit/entries/1?organization=acme", nil)
	if status != http.StatusNotImplemented {
		t.Fatalf("GET an entry with no entry reader answers %d, want %d", status, http.StatusNotImplemented)
	}
}

// scopelessReader answers EntryAt from the chain of one namespace whatever
// scope the caller asks for: a store that does not keep scopes apart.
type scopelessReader struct {
	*memAuditStore

	from namespace.Name
}

func (r scopelessReader) EntryAt(ctx context.Context, _ namespace.Name, seq int64) (audit.Entry, error) {
	return r.memAuditStore.EntryAt(ctx, r.from, seq)
}

// TestReadOneEntryStaysInItsScope proves that the route answers only an
// entry of the scope that the request names: each namespace reads its own
// entry 1, and an entry of another scope from the store answers 404.
func TestReadOneEntryStaysInItsScope(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")
	seedEntry(t, store, "globex", "bob")

	for ns, want := range map[string]string{"acme": "alice", "globex": "bob"} {
		var got struct {
			Subject string `json:"subject"`
		}

		target := "/api/audit/entries/1?organization=" + ns
		if status := getJSON(t, handler, target, &got); status != http.StatusOK || got.Subject != want {
			t.Fatalf("GET %s = %d %q, want 200 %q", target, status, got.Subject, want)
		}
	}

	leaky := newReaderHandler(t, scopelessReader{memAuditStore: store, from: "acme"})

	if status := getJSON(t, leaky, "/api/audit/entries/1?organization=globex", nil); status != http.StatusNotFound {
		t.Fatalf("GET an entry that the store answers from another scope = %d, want %d", status, http.StatusNotFound)
	}
}

// TestReadOneEntryOfAnUnknownNamespaceAnswers404 proves that the route
// answers 404 for a namespace that does not exist, even when the store holds
// an entry under its name.
func TestReadOneEntryOfAnUnknownNamespaceAnswers404(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	entries := &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil}
	seedEntry(t, entries, "ghost", "alice")

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		OnAuditFailure: nil,
		Namespaces:     namespaces,
		Entries:        entries,
		EntryReader:    entries,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		DenialLimiter:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if status := getJSON(t, handler, "/api/audit/entries/1?organization=ghost", nil); status != http.StatusNotFound {
		t.Fatalf("GET an entry of an unknown namespace = %d, want %d", status, http.StatusNotFound)
	}
}

// newReaderHandler builds an enforcing handler that reads one entry with
// reader.
func newReaderHandler(t *testing.T, reader audit.EntryReader) http.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		OnAuditFailure: nil,
		Namespaces:     namespaces,
		Entries:        &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil},
		EntryReader:    reader,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     &recordingAuthorizer{calls: nil},
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		DenialLimiter:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}
