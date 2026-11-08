package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const hostBetaConnectionsPath = "/api/dustpan/v1/namespaces/beta/git-host-connections"

// spoilKeyBinding changes the address and the digest column of the registration
// together, as a writer of the database does. The digest check then passes, and
// only the binding of the sealed key can refuse.
func spoilKeyBinding(t *testing.T, e *hostEnv) {
	t.Helper()

	reg, err := e.store.GitHost().GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get registration: %v", err)
	}

	reg.BaseURL = "https://ghe.example.com/api/v3"
	reg.AddressDigest = forge.AddressDigest(reg.Kind, reg.BaseURL, reg.AppID)

	_, err = e.store.GitHost().PutRegistration(t.Context(), reg, reg.Version, hostRegistrationEntry())
	if err != nil {
		t.Fatalf("put registration: %v", err)
	}
}

// TestGitHostCheckAnswersEachOpenRefusalWithItsOwnCause sends a check for a
// connection that the service refuses to open for a reason of the stored
// registration. Each cause answers its own status and sentence, never the
// sentence for a credential that GitHub refused, and each cause is counted and
// audited as a denied open.
func TestGitHostCheckAnswersEachOpenRefusalWithItsOwnCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spoil   func(t *testing.T, e *hostEnv)
		reason  string
		message string
	}{
		{
			"a key that does not open with the binding of the address", spoilKeyBinding,
			forge.ReasonKeyNotOpened, "cannot open the stored key",
		},
		{
			"an address that the rules refuse", func(_ *testing.T, e *hostEnv) { e.refuseAddress.Store(true) },
			forge.ReasonAddressRefused, "address rules of the server refuse",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newHostEnv(t, hostAllowAll())
			e.register(t)

			id := e.connect(t, "101")
			opened := e.opener.openedCount()

			tc.spoil(t, e)

			r := e.send(t, http.MethodPost, hostConnectionsPath+"/"+id+"/check", "")
			wantHost(t, r, http.StatusConflict)
			wantOpenRefusal(t, e, r, tc.reason, tc.message)

			if e.opener.openedCount() != opened {
				t.Fatal("the adapter opened a connection that the service refused")
			}
		})
	}
}

// wantOpenRefusal checks the sentence of a refused open, and that the service
// counted it with reason and wrote one denied entry.
func wantOpenRefusal(t *testing.T, e *hostEnv, r hostResponse, reason, message string) {
	t.Helper()

	if !strings.Contains(r.body, message) {
		t.Fatalf("answer = %s, want the sentence %q", r.body, message)
	}

	if strings.Contains(r.body, "GitHub refused") || strings.Contains(r.body, "ghe.example.com") {
		t.Fatalf("answer = %s, want no sentence about GitHub and no address", r.body)
	}

	if got := e.refusals(); len(got) != 1 || got[0] != reason {
		t.Fatalf("refusals = %v, want one %q", got, reason)
	}

	if n := countEntries(e.auditEntries(t, "acme"), audit.OutcomeDenied, "git-host-connection:open"); n != 1 {
		t.Fatalf("%d denied open entries, want 1", n)
	}
}

// TestGitHostCheckAuditsItsRefusals sends the checks that the route refuses
// before it reaches GitHub. Each one leaves one failure entry of the check, so
// that a caller who hits the limit over and over leaves a trace.
func TestGitHostCheckAuditsItsRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prep   func(t *testing.T, e *hostEnv, target string)
		target func(id string) string
		header map[string]string
		status int
	}{
		{
			name: "a check inside the interval of the connection",
			prep: func(t *testing.T, e *hostEnv, target string) {
				t.Helper()

				wantHost(t, e.send(t, http.MethodPost, target, ""), http.StatusOK)
			},
			target: func(id string) string { return hostConnectionsPath + "/" + id + "/check" },
			header: nil,
			status: http.StatusTooManyRequests,
		},
		{
			name:   "a connection that does not exist",
			header: nil,
			prep:   func(*testing.T, *hostEnv, string) {},
			target: func(string) string { return hostConnectionsPath + "/" + forge.NewConnectionID() + "/check" },
			status: http.StatusNotFound,
		},
		{
			name:   "an ID that is not a connection ID",
			header: nil,
			prep:   func(*testing.T, *hostEnv, string) {},
			target: func(string) string { return hostConnectionsPath + "/not-an-id/check" },
			status: http.StatusNotFound,
		},
		{
			name:   "an Idempotency-Key",
			prep:   func(*testing.T, *hostEnv, string) {},
			target: func(id string) string { return hostConnectionsPath + "/" + id + "/check" },
			header: map[string]string{"Idempotency-Key": "k-1"},
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limits := forge.DefaultCheckLimits()
			e := newHostEnvLimited(t, hostAllowAll(), withKeys, &limits)
			e.register(t)

			id := e.connect(t, "101")
			target := tc.target(id)
			tc.prep(t, e, target)

			before := countEntries(e.auditEntries(t, "acme"), audit.OutcomeFailure, "git-host-connection:check")

			r := e.do(t, hostCall{headers: tc.header, method: http.MethodPost, target: target, body: ""})
			wantHost(t, r, tc.status)

			after := countEntries(e.auditEntries(t, "acme"), audit.OutcomeFailure, "git-host-connection:check")
			if after != before+1 {
				t.Fatalf("%d failure entries of the check after the refusal, want %d", after, before+1)
			}
		})
	}
}

// TestGitHostRefusedChangesNameTheirConnection sends the changes that the
// routes refuse to the connections of a namespace, and checks that each
// failure entry names the connection of the request. A create that fails names
// no connection: the route never made one.
func TestGitHostRefusedChangesNameTheirConnection(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	first := e.connect(t, "101")
	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/102", `{"namespace":"acme"}`), http.StatusCreated)
	second := e.connect(t, "102")

	// The delete of the default connection is refused while another connection
	// exists.
	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+first, ""), http.StatusConflict)

	// The make-default is refused with a header that is not a version and with
	// a version that the connection does not have.
	for _, tc := range []struct {
		header string
		status int
	}{
		{"abc", http.StatusBadRequest},
		{`"99"`, http.StatusPreconditionFailed},
	} {
		wantHost(t, e.do(t, hostCall{
			headers: map[string]string{"If-Match": tc.header}, method: http.MethodPost,
			target: hostConnectionsPath + "/" + second + "/make-default", body: "",
		}), tc.status)
	}

	// The create of a connection with an auth kind that the server does not
	// support fails before any connection exists.
	wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath,
		`{"kind":"github","auth":"token","installationID":"101"}`), http.StatusBadRequest)

	// A hostile ID holds an encoded newline, an encoded slash and 300
	// characters. The mux routes it to the handler, which refuses it before it
	// reads a connection.
	hostile := "hostile%0Atext%2F" + strings.Repeat("a", 300)

	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+hostile, ""), http.StatusNotFound)
	wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath+"/"+hostile+"/make-default", ""), http.StatusNotFound)

	tests := []struct {
		name   string
		object string
		count  int
	}{
		{"a refused delete", "git-host-connection:delete:" + first, 1},
		{"a refused make-default", "git-host-connection:make-default:" + second, 2},
		{"a refused create", "git-host-connection:create", 1},
		{"a refused delete of a hostile ID", "git-host-connection:delete", 1},
		{"a refused make-default of a hostile ID", "git-host-connection:make-default", 1},
	}

	entries := e.auditEntries(t, "acme")

	// No failure entry holds any part of the hostile ID.
	wantNoHostilePart(t, entries, "hostile", strings.Repeat("a", 300))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, entry := range entries {
				if entry.Outcome == audit.OutcomeFailure && entry.Object == tt.object && entry.Namespace != "acme" {
					t.Fatalf("entry = %+v, want the namespace acme", entry)
				}
			}

			if n := countEntries(entries, audit.OutcomeFailure, tt.object); n != tt.count {
				t.Fatalf("%d failure entries with the object %q, want %d", n, tt.object, tt.count)
			}
		})
	}
}

// TestGitHostConnectionIDOfAnotherNamespaceIsNotFound sends the ID of a
// connection of acme through the routes of beta, as a caller who may act on
// both namespaces. Each route answers 404 and the connection of acme stays as
// it was.
func TestGitHostConnectionIDOfAnotherNamespaceIsNotFound(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAuthorizer{deny: nil, anyNamespace: true})
	e.register(t)

	id := e.connect(t, "101")
	other := hostBetaConnectionsPath + "/" + id
	opened := e.opener.openedCount()

	for _, call := range []struct{ method, target string }{
		{http.MethodGet, other},
		{http.MethodDelete, other},
		{http.MethodPost, other + "/make-default"},
		{http.MethodPost, other + "/check"},
	} {
		r := e.send(t, call.method, call.target, "")
		if r.status != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404: %s", call.method, call.target, r.status, r.body)
		}
	}

	if e.opener.openedCount() != opened {
		t.Fatal("a check through another namespace opened the connection")
	}

	got := e.send(t, http.MethodGet, hostConnectionsPath+"/"+id, "")
	wantHost(t, got, http.StatusOK)

	if doc := decodeHost[connectionDoc](t, got); doc.Namespace != "acme" || doc.Version != 1 || !doc.Default {
		t.Fatalf("connection = %+v, want the connection of acme as created", doc)
	}

	list := e.send(t, http.MethodGet, hostBetaConnectionsPath, "")
	wantHost(t, list, http.StatusOK)

	if n := len(decodeHost[connectionsDoc](t, list).Connections); n != 0 {
		t.Fatalf("beta lists %d connections, want none", n)
	}
}

// foreignStore answers the connections of acme for every namespace, as a store
// with a defect would.
type foreignStore struct {
	forge.Store
}

func (f foreignStore) GetConnection(ctx context.Context, _ namespace.Name, id string) (forge.ConnectionRecord, error) {
	return f.Store.GetConnection(ctx, "acme", id)
}

func (f foreignStore) ListConnections(ctx context.Context, _ namespace.Name) ([]forge.ConnectionRecord, error) {
	return f.Store.ListConnections(ctx, "acme")
}

// TestGitHostReadsCheckTheNamespaceOfTheRowsThemselves lets the store answer
// the rows of another namespace, as if its own check had failed. The handler
// shows none of them: the list is empty and the read answers 404.
func TestGitHostReadsCheckTheNamespaceOfTheRowsThemselves(t *testing.T) {
	t.Parallel()

	e := newHostEnvWrapped(
		t, hostAuthorizer{deny: nil, anyNamespace: true}, withKeys, nil,
		func(s forge.Store) forge.Store { return foreignStore{Store: s} },
	)
	e.register(t)

	id := e.connect(t, "101")

	list := e.send(t, http.MethodGet, hostBetaConnectionsPath, "")
	wantHost(t, list, http.StatusOK)

	if n := len(decodeHost[connectionsDoc](t, list).Connections); n != 0 {
		t.Fatalf("beta lists %d connections of another namespace, want none", n)
	}

	wantHost(t, e.send(t, http.MethodGet, hostBetaConnectionsPath+"/"+id, ""), http.StatusNotFound)
	wantHost(t, e.send(t, http.MethodGet, hostConnectionsPath+"/"+id, ""), http.StatusOK)
}

// TestGitHostRoutesRefuseABodyAbove64KiB sends a body that is one byte above
// the limit to each route that reads a body. The limit is not a status of the
// size of the JSON: the route stops reading and answers 413.
func TestGitHostRoutesRefuseABodyAbove64KiB(t *testing.T) {
	t.Parallel()

	const limit = 64 * 1024

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	pad := strings.Repeat("a", limit)

	for _, call := range []struct{ method, target, body string }{
		{http.MethodPost, hostConnectionsPath, `{"kind":"github","auth":"app","installationID":"101","x":"` + pad + `"}`},
		{http.MethodPut, hostAppPath, `{"appID":"` + hostAppID + `","apiAddress":"` + pad + `"}`},
		{http.MethodPut, hostInstallationsPath + "/102", `{"namespace":"` + pad + `"}`},
	} {
		r := e.send(t, call.method, call.target, call.body)
		if r.status != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s with %d bytes = %d, want 413", call.method, call.target, len(call.body), r.status)
		}
	}

	// A body at the limit is read: the route then refuses its content, not its size.
	atLimit := `{"namespace":"` + strings.Repeat("a", limit-len(`{"namespace":""}`)) + `"}`
	if len(atLimit) != limit {
		t.Fatalf("test body has %d bytes, want %d", len(atLimit), limit)
	}

	r := e.send(t, http.MethodPut, hostInstallationsPath+"/102", atLimit)
	if r.status == http.StatusRequestEntityTooLarge {
		t.Fatalf("a body of %d bytes answered 413, want the limit to start above it", limit)
	}
}

func hostRegistrationEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: audit.ServerScope, Subject: "tester", IDPSubject: "", TokenID: "",
		Action: "git-host-app:put", Object: "git-host-app:github", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// countEntries returns how many entries have the outcome and the object.
func countEntries(entries []audit.Entry, outcome audit.Outcome, object string) int {
	n := 0

	for i := range entries {
		if entries[i].Outcome == outcome && (entries[i].Action == object || entries[i].Object == object) {
			n++
		}
	}

	return n
}

// wantNoHostilePart fails the test when a failure entry of the namespace holds
// part of a hostile path ID.
func wantNoHostilePart(t *testing.T, entries []audit.Entry, parts ...string) {
	t.Helper()

	for i := range entries {
		if entries[i].Outcome != audit.OutcomeFailure {
			continue
		}

		for _, part := range parts {
			if strings.Contains(entries[i].Object, part) {
				t.Fatalf("entry = %+v, want no part %q of the hostile ID", entries[i], part)
			}
		}
	}
}
