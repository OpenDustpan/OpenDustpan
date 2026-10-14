package audit_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// seedServerEntry appends one entry to the scope @server, as the installation
// writes for an action that is not in a namespace (ADR 60, decision 7).
func seedServerEntry(t *testing.T, store *memAuditStore, subject string) {
	t.Helper()

	_, err := store.Append(t.Context(), namespace.Fence{Namespace: audit.ServerScope, FailoverVersion: 1}, audit.Entry{
		At:         changeTime(),
		Namespace:  audit.ServerScope,
		Subject:    subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     "server:manage-settings",
		Object:     "@server",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "opendustpan-cli/test",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "req-" + subject,
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
		Changes:    nil,
		ObjectID:   "",
	})
	if err != nil {
		t.Fatalf("seed @server audit entry: %v", err)
	}
}

// TestServerScopeIsReadWithTheServerAction proves that a read of the scope
// @server authorizes server:read-audit on the installation, and that
// namespace:read-audit does not stand in for it.
func TestServerScopeIsReadWithTheServerAction(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/api/audit/entries", "/api/audit/check"} {
		recorder := &recordingAuthorizer{calls: nil}
		handler, _ := newEnforcingHandler(t, recorder)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(t, path+"?organization=@server"))

		if rec.Code != http.StatusOK {
			t.Fatalf(
				"GET %s?organization=@server answers %d, want %d (%s)",
				path,
				rec.Code,
				http.StatusOK,
				rec.Body.String(),
			)
		}

		want := string(identity.ActionServerReadAudit) + " on @server"
		found := false

		for _, call := range recorder.calls {
			if call.action == identity.ActionNamespaceReadAudit {
				t.Fatalf("GET %s?organization=@server authorized %s, want only the server action", path, call.action)
			}

			if string(call.action)+" on "+call.object == want {
				found = true
			}
		}

		if !found {
			t.Fatalf("GET %s?organization=@server authorized %v, want %q", path, recorder.calls, want)
		}
	}
}

// TestServerScopeNeedsTheServerAction proves that the server action alone
// gates the scope @server: a principal with namespace:read-audit everywhere but
// no server:read-audit gets 403, and one with the server action alone gets 200.
func TestServerScopeNeedsTheServerAction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		deny identity.Action
		want int
	}{
		{name: "server action denied", deny: identity.ActionServerReadAudit, want: http.StatusForbidden},
		{name: "namespace action denied", deny: identity.ActionNamespaceReadAudit, want: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, entries := newEnforcingHandler(
				t,
				selectiveAuthorizer{deny: map[identity.Action]bool{tc.deny: true}},
			)
			seedServerEntry(t, entries, "ops")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, authedRequest(t, "/api/audit/entries?organization=@server"))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestServerScopeListsItsEntries proves the list of @server holds the entries
// of the installation, and not the entries of a namespace.
func TestServerScopeListsItsEntries(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedServerEntry(t, entries, "ops")

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	seedEntry(t, entries, ns, "alice")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/entries?organization=@server"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got listEntriesDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if got.Total != 1 || len(got.Entries) != 1 || got.Entries[0].Subject != "ops" {
		t.Fatalf("@server list = %+v, want the one entry of ops", got)
	}
}
