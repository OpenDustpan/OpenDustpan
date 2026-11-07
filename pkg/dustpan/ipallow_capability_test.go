package dustpan_test

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// capRequest is one capability-route request: its method, path, client
// address, full Authorization header value and body.
type capRequest struct {
	method string
	path   string
	remote string
	auth   string
	body   string
}

// serveCap serves req through srv and answers its status and body.
func serveCap(t *testing.T, srv *dustpan.Server, req capRequest) (int, string) {
	t.Helper()

	httpReq := httptest.NewRequestWithContext(t.Context(), req.method, req.path, strings.NewReader(req.body))
	httpReq.RemoteAddr = req.remote

	if req.auth != "" {
		httpReq.Header.Set("Authorization", req.auth)
	}

	rec := httptest.NewRecorder()
	startedHandler(t, srv).ServeHTTP(rec, httpReq)

	respBody, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return rec.Code, string(respBody)
}

// startPreviewLease creates the stack acme/web/prod, a preview update and
// its start through the handler from inside, and returns the lease token
// with the renew path.
//
//nolint:revive // Two unnamed results keep the helper short: the lease token, the renew path.
func startPreviewLease(t *testing.T, srv *dustpan.Server, inside string) (string, string) {
	t.Helper()

	const access = "token " + adminToken

	call := func(method, path, body string) (int, string) {
		return serveCap(t, srv, capRequest{method: method, path: path, remote: inside, auth: access, body: body})
	}

	if code, body := call(http.MethodPost, "/api/stacks/acme/web", `{"stackName":"prod"}`); code != http.StatusOK {
		t.Fatalf("create stack answers %d, want %d: %s", code, http.StatusOK, body)
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	if code, body := call(http.MethodPost, "/api/stacks/acme/web/prod/preview", `{}`); code != http.StatusOK {
		t.Fatalf("create update answers %d, want %d: %s", code, http.StatusOK, body)
	} else if err := json.Unmarshal([]byte(body), &created); err != nil || created.UpdateID == "" {
		t.Fatalf("create update answers %q, want an update ID: %v", body, err)
	}

	var started struct {
		Token string `json:"token"`
	}

	start := "/api/stacks/acme/web/prod/update/" + created.UpdateID

	if code, body := call(http.MethodPost, start, `{}`); code != http.StatusOK {
		t.Fatalf("start update answers %d, want %d: %s", code, http.StatusOK, body)
	} else if err := json.Unmarshal([]byte(body), &started); err != nil || started.Token == "" {
		t.Fatalf("start update answers %q, want a lease token: %v", body, err)
	}

	return started.Token, start + "/renew_lease"
}

// beginTFEUpload creates the workspace acme/web, locks it and begins a
// pending state version, and returns the state upload and JSON rendering
// upload paths with the capability.
//
//nolint:revive // Two unnamed results keep the helper short: the state path, the rendering path.
func beginTFEUpload(t *testing.T, srv *dustpan.Server, store any) (string, string) {
	t.Helper()

	svc := srv.Workspaces()
	if svc == nil {
		t.Fatal("server has no workspace service")
	}

	ctx := t.Context()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w, err := svc.Create(ctx, workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, store, "acme"))
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, lockErr := svc.Lock(ctx, w.ID, "admin", "apply"); lockErr != nil {
		t.Fatalf("lock workspace: %v", lockErr)
	}

	content := `{"version":4,"serial":1,"lineage":"l1"}`
	sum := md5.Sum([]byte(content)) //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.

	pending, err := svc.BeginState(ctx, w.ID, "admin", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	id := pending.Version.ID.String()
	reveal := pending.Upload.Reveal()

	return "/api/v2/state-uploads/" + id + "/" + reveal,
		"/api/v2/json-state-uploads/" + id + "/" + reveal
}

// badLeaseToken returns a well-formed token that differs from lease: it
// keeps the length and alphabet, so it reaches the lease check and fails
// there.
func badLeaseToken(t *testing.T, lease string) string {
	t.Helper()

	if lease == "" {
		t.Fatal("empty lease token")

		return ""
	}

	last := lease[len(lease)-1]
	replacement := byte('A')

	if last == 'A' {
		replacement = 'B'
	}

	return lease[:len(lease)-1] + string(replacement)
}

// TestIPAllowlistLeaseTokenBeforeGate proves the Pulumi lease check runs
// after the lease token check: a bad update token from outside the list gets
// the same status and body as a bad token for a namespace with no list, a
// valid lease from outside gets 403, and a valid lease from inside passes.
func TestIPAllowlistLeaseTokenBeforeGate(t *testing.T) {
	t.Parallel()

	srv := newIPAllowlistServer(t, true)
	inside := "198.51.100.7:1234"
	outside := "192.0.2.10:5678"

	lease, renew := startPreviewLease(t, srv, inside)

	renewCall := func(remote, tok string) (int, string) {
		return serveCap(t, srv, capRequest{
			method: http.MethodPost,
			path:   renew,
			remote: remote,
			auth:   "update-token " + tok,
			body:   `{"duration":60}`,
		})
	}

	if code, body := renewCall(outside, lease); code != http.StatusForbidden {
		t.Fatalf("valid lease from outside answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if code, body := renewCall(inside, lease); code != http.StatusOK {
		t.Fatalf("valid lease from inside answers %d, want %d: %s", code, http.StatusOK, body)
	}

	bad := badLeaseToken(t, lease)

	withListStatus, withListBody := renewCall(outside, bad)
	if withListStatus != http.StatusUnauthorized {
		t.Fatalf("bad token with a list answers %d, want %d: %s", withListStatus, http.StatusUnauthorized, withListBody)
	}

	deleteReq := ipAPIRequest{
		method: http.MethodDelete,
		path:   "/api/server/namespaces/acme/ip-allowlist",
		remote: inside,
		body:   "",
	}

	deleteStatus, deleteBody := serveIPAllowAPI(t, srv, deleteReq)
	if deleteStatus != http.StatusNoContent {
		t.Fatalf("DELETE list answers %d, want %d: %s", deleteStatus, http.StatusNoContent, deleteBody)
	}

	withoutListStatus, withoutListBody := renewCall(outside, bad)
	if withoutListStatus != withListStatus || withoutListBody != withListBody {
		t.Fatalf(
			"bad token without a list answers %d %q, want %d %q",
			withoutListStatus,
			withoutListBody,
			withListStatus,
			withListBody,
		)
	}

	if code, body := renewCall(outside, lease); code != http.StatusOK {
		t.Fatalf("valid lease without a list answers %d, want %d: %s", code, http.StatusOK, body)
	}
}

// TestIPAllowlistCapabilityRoutes proves the address-only checks of the
// capability routes: with a list for acme, a Pulumi lease renewal and a TFE
// state upload from outside answer 403, and from inside pass the gate.
func TestIPAllowlistCapabilityRoutes(t *testing.T) {
	t.Parallel()

	srv, st := newIPAllowlistServerWith(t, true)
	inside := "198.51.100.7:1234"
	outside := "192.0.2.10:5678"

	lease, renew := startPreviewLease(t, srv, inside)

	renewCall := func(remote string) (int, string) {
		return serveCap(t, srv, capRequest{
			method: http.MethodPost,
			path:   renew,
			remote: remote,
			auth:   "update-token " + lease,
			body:   `{"duration":60}`,
		})
	}

	if code, body := renewCall(outside); code != http.StatusForbidden {
		t.Fatalf("lease renewal from outside answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if code, body := renewCall(inside); code != http.StatusOK {
		t.Fatalf("lease renewal from inside answers %d, want %d: %s", code, http.StatusOK, body)
	}

	state, rendering := beginTFEUpload(t, srv, st)
	content := `{"version":4,"serial":1,"lineage":"l1"}`

	uploadCall := func(remote, target, body string) (int, string) {
		return serveCap(t, srv, capRequest{
			method: http.MethodPut,
			path:   target,
			remote: remote,
			auth:   "",
			body:   body,
		})
	}

	if code, body := uploadCall(outside, state, content); code != http.StatusForbidden {
		t.Fatalf("state upload from outside answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if code, body := uploadCall(inside, state, content); code != http.StatusOK {
		t.Fatalf("state upload from inside answers %d, want %d: %s", code, http.StatusOK, body)
	}

	if code, body := uploadCall(outside, rendering, `{"rendering":1}`); code != http.StatusForbidden {
		t.Fatalf("rendering upload from outside answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if code, body := uploadCall(inside, rendering, `{"rendering":1}`); code != http.StatusOK {
		t.Fatalf("rendering upload from inside answers %d, want %d: %s", code, http.StatusOK, body)
	}
}
