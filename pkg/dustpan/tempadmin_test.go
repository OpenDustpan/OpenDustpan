package dustpan_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// tempadminBase is the fixed start of the clock of the expiry test.
func tempadminBase() time.Time {
	return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC)
}

// The record ends tempadminRecordLife after the create. The host token ends
// at the longest lifetime the host issuer allows, so at tempadminAfterRecord
// the record ended and the token still lives.
const (
	tempadminRecordLife  = 12 * time.Hour
	tempadminAfterRecord = 13 * time.Hour
)

// tempadminEntry returns a valid audit entry in the scope @server for one
// store write of the expiry test at at.
func tempadminEntry(at time.Time, action, object string) audit.Entry {
	return audit.Entry{
		FenceVersion:  0,
		At:            at,
		Namespace:     audit.ServerScope,
		Subject:       tempadmin.HostSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// tempadminUser decodes GET /api/user with the banner flag and its expiry.
type tempadminUser struct {
	GithubLogin            string  `json:"githubLogin"`
	TemporaryAdministrator bool    `json:"temporaryAdministrator"`
	ExpiresAt              *string `json:"expiresAt"`
}

// TestExpiredTempAdminStaysDenied drives an expired temporary administrator
// through the full server stack with the auth gate: the request log and the
// adapter resolve the token twice, and both answers deny the use. The first
// use after the expiry ends the principal, its site-administrator grant and
// its host-recovery token, and keeps the record as a tombstone, so a later
// use never signs in again.
func TestExpiredTempAdminStaysDenied(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	base := tempadminBase()
	now := base
	clock := func() time.Time { return now }

	st := openTempAdminStore(ctx, t)
	blobs := openTempAdminBlobs(t)
	ts := newTempAdminServer(t, st, blobs, clock)

	token := seedTempAdmin(t, st, base, clock)

	callUser := func(token string) (int, tempadminUser) {
		t.Helper()

		return fetchTempAdminUser(ctx, t, ts, token)
	}

	status, user := callUser(token)
	requireWorkingBanner(t, status, user)

	// Past the fixed expiry the token still lives, but the record ended:
	// every answer denies the use.
	now = base.Add(tempadminAfterRecord)

	requireExpiredDenied(t, callUser, token)
	checkTempAdminEnded(t, st, base)
	checkTempAdminAudit(t, st)
}

// openTempAdminStore opens the SQLite store of the expiry test.
func openTempAdminStore(ctx context.Context, t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// openTempAdminBlobs opens the blob store of the expiry test.
func openTempAdminBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// newTempAdminServer starts the full server of the expiry test with the
// auth gate, at the clock clock.
func newTempAdminServer(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, clock func() time.Time,
) *httptest.Server {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithClock(clock),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return ts
}

// fetchTempAdminUser answers GET /api/user with token.
func fetchTempAdminUser(ctx context.Context, t *testing.T, ts *httptest.Server, token string) (int, tempadminUser) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/user", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "token "+token)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/user: %v", err)
	}

	data := readTempAdminBody(t, resp)

	return resp.StatusCode, decodeTempAdminUser(t, resp.StatusCode, data)
}

// readTempAdminBody reads and closes the body of resp.
func readTempAdminBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return data
}

// decodeTempAdminUser decodes the user of a 200 answer, or an empty user.
func decodeTempAdminUser(t *testing.T, status int, data []byte) tempadminUser {
	t.Helper()

	var user tempadminUser

	if status != http.StatusOK {
		return user
	}

	if err := json.Unmarshal(data, &user); err != nil {
		t.Fatalf("decode user: %v (%s)", err, data)
	}

	return user
}

// requireWorkingBanner proves the user endpoint answers the banner flag
// for alice while the record works.
func requireWorkingBanner(t *testing.T, status int, user tempadminUser) {
	t.Helper()

	if status != http.StatusOK || !user.TemporaryAdministrator || user.GithubLogin != "alice" {
		t.Fatalf("GET /api/user = %d, %+v; want 200 for alice with the banner flag", status, user)
	}
}

// requireExpiredDenied proves every use past the expiry answers 401.
func requireExpiredDenied(t *testing.T, callUser func(string) (int, tempadminUser), token string) {
	t.Helper()

	for i := range 2 {
		if status, _ := callUser(token); status != http.StatusUnauthorized {
			t.Fatalf("expired use %d = %d, want 401", i, status)
		}
	}
}

// seedTempAdmin records alice as a temporary administrator at base with a
// site-administrator grant and a personal token that outlives the record,
// and answers the secret of the token.
func seedTempAdmin(t *testing.T, st *sqlite.Store, base time.Time, now func() time.Time) string {
	t.Helper()

	ctx := t.Context()

	p := principal.Principal{
		BreakGlass:  false,
		CreatedAt:   base,
		UpdatedAt:   base,
		DisabledAt:  time.Time{},
		ID:          "prn-alice",
		Subject:     "alice",
		DisplayName: "alice",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
	}

	e := tempadminEntry(base, tempadmin.Action, principal.Ref(p.ID))

	if err := st.CreatePrincipal(ctx, p, e); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	g := access.Grant{
		CreatedAt: base,
		Condition: nil,
		Object:    identity.ServerObject(),
		ID:        "grt-alice",
		Role:      access.RoleSiteAdministrator,
		CreatedBy: tempadmin.HostSubject,
		Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: p.ID},
	}

	grantEntry := tempadminEntry(
		base,
		string(identity.ActionServerManageAccess),
		access.GrantRef(g),
	)
	if err := st.CreateGrant(ctx, g, namespace.Fence{Namespace: "", FailoverVersion: 0}, grantEntry, nil); err != nil {
		t.Fatalf("grant site-administrator: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil,
		Now:    now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	r := tempadmin.TempAdmin{
		CreatedAt:      base,
		ExpiresAt:      base.Add(tempadminRecordLife),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    p.ID,
		Subject:        "alice",
		CreatedBy:      "tester",
		Reason:         "every administrator lost access",
	}

	recordEntry := tempadminEntry(base, tempadmin.Action, tempadmin.Ref(p.ID))
	if createErr := st.CreateTempAdmin(ctx, r, recordEntry); createErr != nil {
		t.Fatalf("record temporary administrator: %v", createErr)
	}

	// The person makes its own token that outlives the record, as a
	// temporary administrator may: only the record expiry ends its use.
	alice := identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}

	_, issued, err := tokens.Create(
		ctx,
		alice,
		tempadminEntry(base, string(identity.ActionTokenManageOwn), ""),
		accesstoken.ScopeOwn,
		accesstoken.Input{ExpiresAt: base.Add(tempadmin.Lifetime), PrincipalID: p.ID, Name: tempadmin.HostTokenName},
	)
	if err != nil {
		t.Fatalf("create the own token: %v", err)
	}

	return issued.Reveal()
}

// checkTempAdminEnded proves the expiry ended the principal, its
// site-administrator grant and its host-recovery token, and kept the
// record as a tombstone.
func checkTempAdminEnded(t *testing.T, st *sqlite.Store, base time.Time) {
	t.Helper()

	ctx := t.Context()

	p, err := st.GetPrincipal(ctx, "prn-alice")
	if err != nil {
		t.Fatalf("get principal: %v", err)
	}

	if !p.DisabledAt.Equal(base.Add(tempadminAfterRecord)) {
		t.Fatalf("principal disabled at = %s, want the time of the first expired use", p.DisabledAt)
	}

	grants, err := st.GrantsOn(
		ctx,
		[]access.Subject{{Kind: access.SubjectPrincipal, ID: "prn-alice"}},
		[]identity.Object{identity.ServerObject()},
	)
	if err != nil {
		t.Fatalf("grants after expiry: %v", err)
	}

	if len(grants) != 0 {
		t.Fatalf("grants after expiry = %+v, want none live", grants)
	}

	rec, err := st.GetTempAdmin(ctx, "prn-alice")
	if err != nil {
		t.Fatalf("record after expiry = %v, want the tombstone", err)
	}

	if rec.ActiveAt(base.Add(tempadminAfterRecord)) {
		t.Fatal("tombstone is still active after its expiry")
	}
}

// checkTempAdminAudit proves the audit trail holds exactly one sign-in
// entry for the working request, and exactly one expiry entry for the
// first use after the expiry.
func checkTempAdminAudit(t *testing.T, st *sqlite.Store) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	var signIns, expiries int

	for i := range listing.Entries {
		e := listing.Entries[i]

		if e.Action != tempadmin.Action {
			continue
		}

		switch e.Subject {
		case "alice":
			signIns++

			if e.Outcome != audit.OutcomeSuccess {
				t.Fatalf("sign-in entry = %+v, want a success outcome", e)
			}

			if e.SourceIP == "" || e.RequestID == "" {
				t.Fatalf("sign-in entry = %+v, want the client address and the request ID", e)
			}
		case tempadmin.SweepSubject:
			expiries++
		default:
		}
	}

	if signIns != 1 {
		t.Fatalf("sign-in entries = %d, want one for the working request", signIns)
	}

	if expiries != 1 {
		t.Fatalf("expiry entries = %d, want one for the first expired use", expiries)
	}
}
