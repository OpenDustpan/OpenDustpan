package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/forgelinksvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// subjectAuthenticator signs a token in as the subject the token names:
// "admin" is the bootstrap administrator, and every other token is a
// principal with no administrator flag.
type subjectAuthenticator struct{}

func (subjectAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        token,
		DisplayName:    token,
		Admin:          token == "admin",
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
}

// roleDoc and grantDoc decode the wire forms in tests.
type roleDoc struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
	BuiltIn bool     `json:"builtIn"`
}

type roleListDoc struct {
	Roles []roleDoc `json:"roles"`
}

type grantDoc struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Role      string `json:"role"`
	Object    string `json:"object"`
	CreatedBy string `json:"createdBy"`
}

type grantListDoc struct {
	Grants     []grantDoc `json:"grants"`
	NextCursor string     `json:"nextCursor"`
	Total      int64      `json:"total"`
}

// accessFixture is a handler that decides with grants, over one store.
type accessFixture struct {
	store *sqlite.Store
	// path is the database file of store. A test opens a second connection to
	// it to plant a row that no port writes.
	path string

	sessions *session.Sessions
	handler  testHandler
	// passwords is the password budget of the handler: it counts each
	// failure of a password or code check.
	passwords *fakeLimiter
}

// newAccessFixture builds a handler whose authorizer and access service
// read the grants of a fresh SQLite store. The store holds the namespace
// acme and the principals alice (admin on acme), olga (installation
// operator), ann (account administrator) and bob (no grant).
func newAccessFixture(t *testing.T) accessFixture {
	t.Helper()

	return newAccessFixtureWith(t, func(st *sqlite.Store) auditStore { return st })
}

// auditStore keeps the entries of @server and of each namespace, as the
// handler of the access review needs.
type auditStore interface {
	audit.ServerStore
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// newAccessFixtureWith builds the fixture of newAccessFixture with the
// audit store that entries returns for the store.
func newAccessFixtureWith(t *testing.T, entries func(st *sqlite.Store) auditStore) accessFixture {
	t.Helper()

	return newAccessFixtureFull(t, entries, nil)
}

// newAccessFixtureFull builds the fixture of newAccessFixtureWith with the
// passkey service passkeys. With nil, the handler gets a real passkey service
// over the store.
func newAccessFixtureFull(
	t *testing.T, entries func(st *sqlite.Store) auditStore, passkeys apiserver.PasskeyService,
) accessFixture {
	t.Helper()

	return newAccessFixtureSignIn(t, entries, passkeys, nil)
}

// newAccessFixtureSignIn builds the fixture of newAccessFixtureFull and lets
// tweak change the console sign-in before the handler reads it. With nil, the
// sign-in stays as it is.
func newAccessFixtureSignIn(
	t *testing.T, entries func(st *sqlite.Store) auditStore, passkeys apiserver.PasskeyService,
	tweak func(in *apiserver.SignIn, st *sqlite.Store),
) accessFixture {
	t.Helper()

	return newAccessFixtureAll(t, entries, passkeys, tweak, noGitHostLinks)
}

// noGitHostLinks gives no link service: the routes of the links are not mounted.
func noGitHostLinks(*sqlite.Store) *forgelinksvc.Service { return nil }

// gitHostLinksOf gives the link service of the store: the routes of the links
// are mounted.
func gitHostLinksOf(st *sqlite.Store) *forgelinksvc.Service {
	return forgelinksvc.NewService(st.ForgeLinks(), st, nil, nil).WithIntake()
}

// newAccessFixtureLinks is newAccessFixtureWith with the link service that links
// gives for the store.
func newAccessFixtureLinks(
	t *testing.T, entries func(st *sqlite.Store) auditStore, links func(*sqlite.Store) *forgelinksvc.Service,
) accessFixture {
	t.Helper()

	return newAccessFixtureAll(t, entries, nil, nil, links)
}

// newAccessFixtureAll builds the fixture with the passkey service passkeys, the
// sign-in change tweak and the link service that links gives for the store.
func newAccessFixtureAll(
	t *testing.T, entries func(st *sqlite.Store) auditStore, passkeys apiserver.PasskeyService,
	tweak func(in *apiserver.SignIn, st *sqlite.Store), links func(*sqlite.Store) *forgelinksvc.Service,
) accessFixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	st := openServerTemplateStore(t, path)

	if passkeys == nil {
		passkeys = newRoutePasskeys(t, st)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, seedErr := namespaces.Create(t.Context(), "acme"); seedErr != nil {
		t.Fatalf("seed namespace: %v", seedErr)
	}

	seedStack(t, st, "acme", "web", "prod")

	authz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: st, Callers: st, Collections: stubCollections{}, Units: st, Principals: st},
	)
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	grants, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: stubCollections{}, Units: st,
		},
		Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      st,
			Guard:      grants,
			GroupReach: grants,
			Namespaces: st,
			Tokens:     grants,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: nil, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: stubCollections{}, Units: st,
		},
		Logger: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	sessions, err := session.NewSessions(session.SessionsConfig{Store: st, Providers: st, Inactivity: nil, Now: nil})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	passwords := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}

	signIn := &apiserver.SignIn{
		Service:             newTestSignInService(t, st, sessions, principals),
		OnDevRefused:        nil,
		DevCode:             nil,
		DevAdmin:            noBootstrapAdmin,
		Sessions:            sessions,
		Limiter:             nil,
		Stored:              nil,
		Admin:               nil,
		PasswordProviders:   nil,
		Origins:             []string{signInBase},
		Providers:           nil,
		PasswordLimiter:     passwords,
		Passkeys:            passkeys,
		PasskeySignIn:       nil,
		PasskeyBeginLimiter: nil,
		PasskeyLimiter:      nil,
	}
	if tweak != nil {
		tweak(signIn, st)
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		LocalUsers:       newRouteLocalUsers(t, st),
		Preferences:      nil,
		GitHostLinks:     links(st),
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    subjectAuthenticator{},
		Authorizer:       authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       principals,
		Reach:            routeReach(st),
		Tokens:           tokens,
		Access:           grants,
		Audit:            entries(st),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           signIn,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	f := accessFixture{store: st, path: path, sessions: sessions, handler: handler, passwords: passwords}

	f.seed(t, "alice", access.RoleAdmin, identity.NamespaceObject("acme"))
	f.seed(t, "olga", access.RoleInstallationOperator, identity.ServerObject())
	f.seed(t, "ann", access.RoleAccountAdministrator, identity.ServerObject())
	f.seed(t, "bob", "", identity.Object{})
	return f
}

// seed stores the principal "prn-<subject>" and, when role is set, its
// grant of role on obj.
func (f accessFixture) seed(t *testing.T, subject, role string, obj identity.Object) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	prn := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          "prn-" + subject,
		Subject:     subject,
		DisplayName: "",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
		DisabledAt:  time.Time{},
	}
	if err := f.store.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID)); err != nil {
		t.Fatalf("seed principal: %v", err)
	}

	if role == "" {
		return
	}

	f.grant(t, subject, role, obj)
}

// grant writes the grant of role on obj to the principal "prn-<subject>".
func (f accessFixture) grant(t *testing.T, subject, role string, obj identity.Object) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-seed-" + subject, Role: role, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-" + subject},
	}
	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:"+grt.ID)
	if err := f.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
}

// as sends one request with the token of subject and fails unless it
// answers c.want. It decodes a JSON answer into out when out is not nil. A
// change of roles or grants that the caller may make uses a console session
// of subject with a recent sign-in: an access token never gives the fresh
// authentication that such a change needs.
func (f accessFixture) as(t *testing.T, subject string, c apiCall, out any) {
	t.Helper()

	if needsFreshSession(c.method, c.path) {
		f.asFresh(t, subject, c, out)

		return
	}

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	req.Header.Set("Authorization", "Bearer "+subject)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != c.want {
		t.Fatalf("%s %s %s answers %d, want %d (%s)", subject, c.method, c.path, rec.Code, c.want, rec.Body.String())
	}

	if out != nil {
		decodeInto(t, rec.Body.String(), out)
	}
}

func TestRoleRoutes(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var list roleListDoc
	f.as(t, "bob", apiCall{http.MethodGet, "/api/server/roles", "", http.StatusOK}, &list)

	if len(list.Roles) != len(access.BuiltInRoles()) || !list.Roles[0].BuiltIn {
		t.Fatalf("roles = %+v, want the built-in roles", list.Roles)
	}

	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/roles", `{"name":"deployer","actions":["unit:read"]}`, http.StatusForbidden,
	}, nil)

	var role roleDoc
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/roles", `{"name":"deployer","actions":["unit:write","unit:read"]}`,
		http.StatusCreated,
	}, &role)

	if !strings.HasPrefix(role.ID, "rol-") || strings.Join(role.Actions, " ") != "unit:read unit:write" {
		t.Fatalf("role = %+v, want a custom role with sorted actions", role)
	}

	for _, c := range []apiCall{
		{http.MethodPost, "/api/server/roles", `{"name":"Deployer","actions":["unit:read"]}`, http.StatusConflict},
		{http.MethodPost, "/api/server/roles", `{"name":"viewer","actions":["unit:read"]}`, http.StatusBadRequest},
		{http.MethodPost, "/api/server/roles", `{"name":"x","actions":["unit:fly"]}`, http.StatusBadRequest},
		{http.MethodPost, "/api/server/roles", `{"name":"x"}`, http.StatusBadRequest},
		{http.MethodPatch, "/api/server/roles/viewer", `{"description":"x"}`, http.StatusConflict},
		{http.MethodDelete, "/api/server/roles/viewer", "", http.StatusConflict},
		{http.MethodPatch, "/api/server/roles/" + role.ID, `{"actions":["unit:read"]}`, http.StatusOK},
		{http.MethodGet, "/api/server/roles/" + role.ID, "", http.StatusOK},
		{http.MethodGet, "/api/server/roles/rol-missing", "", http.StatusNotFound},
	} {
		f.as(t, "ann", c, nil)
	}

	f.as(t, "ann", apiCall{
		http.MethodPost,
		"/api/server/namespaces/acme/grants",
		`{"subject":"principal:prn-bob","role":"` + role.ID + `"}`,
		http.StatusCreated,
	}, nil)
	f.as(t, "ann", apiCall{http.MethodDelete, "/api/server/roles/" + role.ID, "", http.StatusConflict}, nil)
}

func TestGrantRoutes(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var g grantDoc
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/namespaces/acme/unit-grants?tool=pulumi&unit=web%2Fprod",
		`{"subject":"principal:prn-bob","role":"writer"}`, http.StatusCreated,
	}, &g)

	if g.Object != "pulumi:acme/web/prod" || g.Subject != "principal:prn-bob" || g.CreatedBy != "alice" {
		t.Fatalf("grant = %+v, want writer on the unit to bob by alice", g)
	}

	var list grantListDoc
	f.as(t, "alice", apiCall{http.MethodGet, "/api/server/namespaces/acme/grants", "", http.StatusOK}, &list)

	if len(list.Grants) != 1 || list.Grants[0].Role != access.RoleAdmin {
		t.Fatalf("namespace grants = %+v, want the admin grant of alice", list.Grants)
	}

	for _, c := range []apiCall{
		{
			http.MethodPost, "/api/server/namespaces/acme/collections/payments/grants",
			`{"subject":"group:grp-ghost#member","role":"viewer"}`, http.StatusNotFound,
		},
		{
			http.MethodPost, "/api/server/namespaces/acme/grants",
			`{"subject":"bob","role":"viewer"}`, http.StatusBadRequest,
		},
		{
			http.MethodPost, "/api/server/namespaces/acme/grants",
			`{"subject":"principal:prn-bob","role":"account-administrator"}`, http.StatusBadRequest,
		},
		{http.MethodPost, "/api/server/namespaces/acme/unit-grants", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/server/grants", `{"subject":"principal:prn-bob","role":"viewer"}`, http.StatusForbidden},
		{http.MethodDelete, "/api/server/namespaces/acme/grants/" + g.ID, "", http.StatusNotFound},
		{
			http.MethodDelete, "/api/server/namespaces/acme/unit-grants/" + g.ID + "?tool=pulumi&unit=web%2Fprod", "",
			http.StatusNoContent,
		},
	} {
		f.as(t, "alice", c, nil)
	}

	// ann holds no operator action, so she cannot give one.
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/grants", `{"subject":"principal:prn-bob","role":"installation-operator"}`,
		http.StatusForbidden,
	}, nil)
}

// TestRevokeGrantByID proves that DELETE /api/server/grants/{id} ends any
// grant by its ID: a namespace grant answers 204 and is gone, an unknown ID
// answers 404, a caller without read on the object gets 404 with the same
// body as the unknown ID, a caller with read but without manage-access gets
// 403, and an installation grant still answers 204.
func TestRevokeGrantByID(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	send := func(subject, method, path, body string) (int, string) {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+subject)

		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)

		return rec.Code, rec.Body.String()
	}

	var g grantDoc
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/namespaces/acme/grants",
		`{"subject":"principal:prn-bob","role":"viewer"}`, http.StatusCreated,
	}, &g)

	status, body := send("alice", http.MethodDelete, "/api/server/grants/"+g.ID, "")
	if status != http.StatusNoContent {
		t.Fatalf("revoke namespace grant by ID answers %d, want %d (%s)", status, http.StatusNoContent, body)
	}

	var list grantListDoc
	f.as(t, "alice", apiCall{http.MethodGet, "/api/server/namespaces/acme/grants", "", http.StatusOK}, &list)

	for i := range list.Grants {
		if list.Grants[i].ID == g.ID {
			t.Fatalf("grants on acme still hold %s after the revoke by ID", g.ID)
		}
	}

	unknownStatus, unknownBody := send("alice", http.MethodDelete, "/api/server/grants/grt-unknown-id", "")
	if unknownStatus != http.StatusNotFound {
		t.Fatalf("revoke unknown grant by ID answers %d, want %d (%s)", unknownStatus, http.StatusNotFound, unknownBody)
	}

	var hidden grantDoc
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/namespaces/acme/grants",
		`{"subject":"principal:prn-olga","role":"viewer"}`, http.StatusCreated,
	}, &hidden)

	status, body = send("bob", http.MethodDelete, "/api/server/grants/"+hidden.ID, "")
	if status != http.StatusNotFound {
		t.Fatalf("revoke without read answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	want := strings.ReplaceAll(unknownBody, "grt-unknown-id", hidden.ID)
	if body != want {
		t.Fatalf("revoke without read body = %s, want %s", body, want)
	}

	f.seed(t, "carol", access.RoleViewer, identity.NamespaceObject("acme"))

	status, body = send("carol", http.MethodDelete, "/api/server/grants/"+hidden.ID, "")
	if status != http.StatusForbidden {
		t.Fatalf("revoke with read but without manage-access answers %d, want %d (%s)",
			status, http.StatusForbidden, body)
	}

	var inst grantDoc
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/grants", `{"subject":"principal:prn-bob","role":"viewer"}`,
		http.StatusCreated,
	}, &inst)

	status, body = send("ann", http.MethodDelete, "/api/server/grants/"+inst.ID, "")
	if status != http.StatusNoContent {
		t.Fatalf("revoke installation grant by ID answers %d, want %d (%s)", status, http.StatusNoContent, body)
	}
}

// deleteGrantByID sends DELETE /api/server/grants/{id} with the test token
// on a fresh handler that denies deny, and answers its status and body.
// Each call builds a fresh handler, so a revoked grant stays present.
func deleteGrantByID(t *testing.T, deny map[identity.Action]bool, id string) (int, string) {
	t.Helper()

	h := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/server/grants/"+id, http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// TestRevokeGrantByIDKindActions proves that DELETE /api/server/grants/{id}
// checks the manage-access action of the grant's own object, for each object
// kind: denying manage answers 403 while read is allowed, and denying read
// answers 404 with the same body as an unknown ID. An unknown ID answers
// 404.
func TestRevokeGrantByIDKindActions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		grantID string
		manage  identity.Action
		read    identity.Action
	}{
		{
			name: "installation", grantID: testServerGrantID,
			manage: identity.ActionServerManageAccess, read: identity.ActionServerReadAccess,
		},
		{
			name: "namespace", grantID: testNamespaceGrantID,
			manage: identity.ActionNamespaceManageAccess, read: identity.ActionNamespaceRead,
		},
		{
			name: "collection", grantID: testCollectionGrantID,
			manage: identity.ActionCollectionManageAccess, read: identity.ActionCollectionRead,
		},
		{
			name: "unit", grantID: testUnitGrantID,
			manage: identity.ActionUnitManageAccess, read: identity.ActionUnitRead,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkGrantKindActions(t, tc.grantID, tc.manage, tc.read)
		})
	}

	if status, body := deleteGrantByID(t, map[identity.Action]bool{}, "grt-unknown-id"); status != http.StatusNotFound {
		t.Fatalf("revoke unknown grant by ID answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}
}

// checkGrantKindActions revokes one grant by ID under each authorizer: 204
// when allowed, 403 when its manage action is denied but read is allowed,
// and 404 with the unknown-ID body when its read action is denied.
func checkGrantKindActions(t *testing.T, grantID string, manage, read identity.Action) {
	t.Helper()

	if status, body := deleteGrantByID(t, map[identity.Action]bool{}, grantID); status != http.StatusNoContent {
		t.Fatalf("revoke %s by ID answers %d, want %d (%s)", grantID, status, http.StatusNoContent, body)
	}

	denyManage := map[identity.Action]bool{manage: true}
	if status, body := deleteGrantByID(t, denyManage, grantID); status != http.StatusForbidden {
		t.Fatalf("revoke %s without manage answers %d, want %d (%s)", grantID, status, http.StatusForbidden, body)
	}

	_, unknownBody := deleteGrantByID(t, map[identity.Action]bool{}, "grt-unknown-id")

	denyRead := map[identity.Action]bool{read: true}

	status, body := deleteGrantByID(t, denyRead, grantID)
	if status != http.StatusNotFound {
		t.Fatalf("revoke %s without read answers %d, want %d (%s)", grantID, status, http.StatusNotFound, body)
	}

	if want := strings.ReplaceAll(unknownBody, "grt-unknown-id", grantID); body != want {
		t.Fatalf("revoke %s without read body = %s, want %s", grantID, body, want)
	}
}

// TestOperatorRefusals checks the families over HTTP: an installation
// operator cannot make a principal and cannot grant.
func TestOperatorRefusals(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	for _, c := range []apiCall{
		{http.MethodPost, "/api/server/principals", `{"subject":"eve","kind":"person"}`, http.StatusForbidden},
		{http.MethodGet, "/api/server/principals", "", http.StatusForbidden},
		{http.MethodPost, "/api/server/grants", `{"subject":"principal:prn-olga","role":"viewer"}`, http.StatusForbidden},
		{
			http.MethodPost, "/api/server/namespaces/acme/grants", `{"subject":"principal:prn-olga","role":"viewer"}`,
			http.StatusForbidden,
		},
		{http.MethodPost, "/api/server/namespaces", `{"name":"ops"}`, http.StatusForbidden},
		{http.MethodGet, "/api/server/maintenance", "", http.StatusOK},
	} {
		f.as(t, "olga", c, nil)
	}
}

// TestNamespaceCreateGrantsTheCreator checks that the creator of a namespace
// holds the namespace-admin role on it, that the bootstrap admin gets no
// grant, and that the grant writes an audit entry.
func TestNamespaceCreateGrantsTheCreator(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	f.as(t, "alice", apiCall{http.MethodPost, "/api/server/namespaces", `{"name":"beta"}`, http.StatusForbidden}, nil)
	f.as(t, "ann", apiCall{http.MethodPost, "/api/server/namespaces", `{"name":"gamma"}`, http.StatusCreated}, nil)
	f.as(t, "admin", apiCall{http.MethodPost, "/api/server/namespaces", `{"name":"delta"}`, http.StatusCreated}, nil)

	var list grantListDoc
	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/namespaces/gamma/grants", "", http.StatusOK}, &list)

	if len(list.Grants) != 1 || list.Grants[0].Subject != "principal:prn-ann" ||
		list.Grants[0].Role != access.RoleAdmin {
		t.Fatalf("gamma grants = %+v, want admin to ann", list.Grants)
	}

	f.as(t, "admin", apiCall{http.MethodGet, "/api/server/namespaces/delta/grants", "", http.StatusOK}, &list)

	if len(list.Grants) != 0 {
		t.Fatalf("delta grants = %+v, want none", list.Grants)
	}

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	// Newest first: the create of delta by the bootstrap admin, then the
	// create of gamma with the grant of ann. The seeds come before them.
	want := []string{"namespace:delta", "grant:" + list0(t, f).ID + " principal:prn-ann admin gamma"}
	if len(listing.Entries) < len(want) {
		t.Fatalf("server entries = %+v, want at least %d", listing.Entries, len(want))
	}

	for i, object := range want {
		e := listing.Entries[i]
		if e.Object != object || e.Action != string(identity.ActionServerCreateNamespace) {
			t.Errorf("entry %d = %+v, want the namespace create %q", i, e, object)
		}
	}
}

// list0 returns the one grant on gamma.
func list0(t *testing.T, f accessFixture) grantDoc {
	t.Helper()

	var list grantListDoc
	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/namespaces/gamma/grants", "", http.StatusOK}, &list)

	return list.Grants[0]
}

// TestGrantChangesWriteAuditEntries checks one entry for each grant and
// revoke, with the whole tuple, and one failure entry for a refused grant.
func TestGrantChangesWriteAuditEntries(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var g grantDoc
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/namespaces/acme/grants", `{"subject":"principal:prn-bob","role":"viewer"}`,
		http.StatusCreated,
	}, &g)
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/namespaces/acme/grants", `{"subject":"principal:prn-bob","role":"viewer"}`,
		http.StatusConflict,
	}, nil)
	f.as(
		t,
		"alice",
		apiCall{http.MethodDelete, "/api/server/namespaces/acme/grants/" + g.ID, "", http.StatusNoContent},
		nil,
	)

	// A grant on a namespace writes its entries in that namespace, so the
	// auditor of the namespace sees them.
	listing, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list acme entries: %v", err)
	}

	tuple := "grant:" + g.ID + " principal:prn-bob viewer acme"
	want := []struct {
		object  string
		outcome audit.Outcome
	}{
		{tuple, audit.OutcomeSuccess},
		{"grants acme principal:prn-bob viewer", audit.OutcomeFailure},
		{tuple, audit.OutcomeSuccess},
	}

	// Newest first. The seeds come before these entries.
	if len(listing.Entries) < len(want) {
		t.Fatalf("acme entries = %+v, want at least %d", listing.Entries, len(want))
	}

	for i, w := range want {
		e := listing.Entries[i]
		if e.Object != w.object || e.Outcome != w.outcome || e.Subject != "alice" ||
			e.Action != string(identity.ActionNamespaceManageAccess) {
			t.Errorf("entry %d = %+v, want %q %s", i, e, w.object, w.outcome)
		}
	}
}

func TestAccessRoutesWithoutStore(t *testing.T) {
	t.Parallel()

	h := newPrincipalHandler(t, nil)

	for _, c := range []apiCall{
		{http.MethodGet, "/api/server/roles", "", http.StatusNotImplemented},
		{http.MethodGet, "/api/server/grants", "", http.StatusNotImplemented},
	} {
		if status, body := call(t, h, c); status != c.want {
			t.Fatalf("%s %s answers %d, want %d (%s)", c.method, c.path, status, c.want, body)
		}
	}
}

// decodeInto decodes the JSON text body into out.
func decodeInto(t *testing.T, body string, out any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

func TestGrantPaging(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	for _, subject := range []string{"olga", "ann", "bob"} {
		f.as(t, "alice", apiCall{
			http.MethodPost, "/api/server/namespaces/acme/grants",
			`{"subject":"principal:prn-` + subject + `","role":"viewer"}`, http.StatusCreated,
		}, nil)
	}

	var got []string

	path := "/api/server/namespaces/acme/grants?limit=2"

	for path != "" {
		var page grantListDoc
		f.as(t, "alice", apiCall{http.MethodGet, path, "", http.StatusOK}, &page)

		if page.Total != 4 {
			t.Fatalf("total = %d, want 4", page.Total)
		}

		for i := range page.Grants {
			got = append(got, page.Grants[i].Subject+" "+page.Grants[i].Role)
		}

		path = ""
		if page.NextCursor != "" {
			path = "/api/server/namespaces/acme/grants?limit=2&cursor=" + page.NextCursor
		}
	}

	want := []string{
		"principal:prn-alice admin",
		"principal:prn-ann viewer",
		"principal:prn-bob viewer",
		"principal:prn-olga viewer",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pages = %q, want %q", got, want)
	}

	refusals := []string{
		"limit=0", "limit=0x1", "limit=1001", "cursor=%21",
		"cursor=YQBiAGM", // a NUL in the role: "a\x00b\x00c"
		"cursor=_wBy",    // not UTF-8: "\xff\x00r"
		"cursor=" + strings.Repeat("A", 513),
	}
	for _, bad := range refusals {
		f.as(t, "alice", apiCall{
			http.MethodGet, "/api/server/namespaces/acme/grants?" + bad, "", http.StatusBadRequest,
		}, nil)
	}
}

// TestAccountAdministratorCannotReachOperatorActions proves the three
// refusals of the security review over HTTP: an account administrator
// cannot make a role with an operator action, cannot join a group that
// holds site-administrator, and cannot revoke a site-administrator grant.
// Each answers 403.
func TestAccountAdministratorCannotReachOperatorActions(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var ops struct {
		ID string `json:"id"`
	}
	f.as(t, "admin", apiCall{http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated}, &ops)

	var grant grantDoc
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/grants",
		`{"subject":"group:` + ops.ID + `#member","role":"site-administrator"}`, http.StatusCreated,
	}, &grant)

	for _, c := range []apiCall{
		{http.MethodPost, "/api/server/roles", `{"name":"restorer","actions":["server:restore"]}`, http.StatusForbidden},
		{
			http.MethodPost, "/api/server/groups/" + ops.ID + "/members", `{"principalID":"prn-ann"}`,
			http.StatusForbidden,
		},
		{http.MethodDelete, "/api/server/grants/" + grant.ID, "", http.StatusForbidden},
		{http.MethodDelete, "/api/server/groups/" + ops.ID, "", http.StatusForbidden},
	} {
		f.as(t, "ann", c, nil)
	}

	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/metrics", "", http.StatusForbidden}, nil)
}

// TestEnableNeedsGrantActions proves that the enable route needs every
// action that the grants of the principal give: an account administrator
// cannot enable a disabled site administrator, but a site administrator
// that is not a bootstrap admin can.
func TestEnableNeedsGrantActions(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seed(t, "boss", access.RoleSiteAdministrator, identity.ServerObject())
	f.seed(t, "sam", access.RoleSiteAdministrator, identity.ServerObject())

	_, err := f.store.SweepInactive(t.Context(), accesstoken.Sweep{
		Inactivity: time.Hour,
		Tokens:     false,
		Accounts:   true,
	}, storetest.ServerChange)
	if err != nil {
		t.Fatalf("sweep inactive: %v", err)
	}

	f.as(t, "ann", apiCall{
		method: http.MethodPost, path: "/api/server/principals/prn-boss/enable", body: "",
		want: http.StatusForbidden,
	}, nil)

	boss, err := f.store.GetPrincipal(t.Context(), "prn-boss")
	if err != nil {
		t.Fatalf("get boss: %v", err)
	}

	if boss.DisabledAt.IsZero() {
		t.Fatal("boss is enabled after a refused enable, want disabled")
	}

	f.as(t, "sam", apiCall{
		method: http.MethodPost, path: "/api/server/principals/prn-boss/enable", body: "",
		want: http.StatusOK,
	}, nil)

	boss, err = f.store.GetPrincipal(t.Context(), "prn-boss")
	if err != nil {
		t.Fatalf("get boss: %v", err)
	}

	if !boss.DisabledAt.IsZero() {
		t.Fatal("boss is disabled after an allowed enable, want enabled")
	}
}

// TestDisableNeedsGrantActions proves that the disable route needs every
// action that the grants of the principal give: an account administrator
// cannot disable a site administrator, but a site administrator that is not
// a bootstrap admin can.
func TestDisableNeedsGrantActions(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seed(t, "boss", access.RoleSiteAdministrator, identity.ServerObject())
	f.seed(t, "sam", access.RoleSiteAdministrator, identity.ServerObject())

	f.as(t, "ann", apiCall{
		method: http.MethodPost, path: "/api/server/principals/prn-boss/disable", body: "",
		want: http.StatusForbidden,
	}, nil)

	boss, err := f.store.GetPrincipal(t.Context(), "prn-boss")
	if err != nil {
		t.Fatalf("get boss: %v", err)
	}

	if !boss.DisabledAt.IsZero() {
		t.Fatal("boss is disabled after a refused disable, want enabled")
	}

	f.as(t, "sam", apiCall{
		method: http.MethodPost, path: "/api/server/principals/prn-boss/disable", body: "",
		want: http.StatusOK,
	}, nil)

	boss, err = f.store.GetPrincipal(t.Context(), "prn-boss")
	if err != nil {
		t.Fatalf("get boss: %v", err)
	}

	if boss.DisabledAt.IsZero() {
		t.Fatal("boss is enabled after an allowed disable, want disabled")
	}
}

// TestGrantListNamesHoldersAndFilters lists the grants on acme with the
// holder of each, and with the holder and role filters.
func TestGrantListNamesHoldersAndFilters(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var all listedGrantListDoc
	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/namespaces/acme/grants", "", http.StatusOK}, &all)

	if len(all.Grants) != 1 || all.Grants[0].HolderName != "alice" || all.Grants[0].HolderKind != "person" {
		t.Fatalf("grants on acme = %+v, want the grant of alice, a person", all)
	}

	for _, c := range []struct {
		query string
		want  int
	}{
		{"holder=principal:prn-alice", 1},
		{"holder=principal:prn-bob", 0},
		{"role=admin", 1},
		{"role=viewer", 0},
		{"holder=principal:prn-alice&role=admin", 1},
	} {
		var page listedGrantListDoc
		f.as(
			t,
			"ann",
			apiCall{http.MethodGet, "/api/server/namespaces/acme/grants?" + c.query, "", http.StatusOK},
			&page,
		)

		if len(page.Grants) != c.want || page.Total != int64(c.want) {
			t.Errorf("grants with %s = %+v, want %d", c.query, page, c.want)
		}
	}

	for _, query := range []string{
		"holder=alice", "holder=group:x", "role=" + strings.Repeat("r", 129), "role=bad%20role", "role=.viewer",
	} {
		f.as(t, "ann", apiCall{
			http.MethodGet, "/api/server/namespaces/acme/grants?" + query, "", http.StatusBadRequest,
		}, nil)
	}
}

// listedGrantDoc and listedGrantListDoc decode a grant list with the
// holders.
type listedGrantDoc struct {
	ID         string `json:"id"`
	HolderName string `json:"holderName"`
	HolderKind string `json:"holderKind"`
}

type listedGrantListDoc struct {
	Grants []listedGrantDoc `json:"grants"`
	Total  int64            `json:"total"`
}

// TestEscalationRefusalIsAudited proves the domain layer of the denial audit:
// a grant that gives more than the granter holds answers 403 and writes a
// denied entry with the reason of the escalation.
func TestEscalationRefusalIsAudited(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	// ann holds no operator action, so she cannot give one.
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/grants", `{"subject":"principal:prn-bob","role":"installation-operator"}`,
		http.StatusForbidden,
	}, nil)

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var denied []audit.Entry

	for _, e := range listing.Entries {
		if e.Outcome == audit.OutcomeDenied && e.Subject == "ann" {
			denied = append(denied, e)
		}
	}

	if len(denied) != 1 {
		t.Fatalf("denied entries of ann = %+v, want one", denied)
	}

	if !strings.Contains(denied[0].Reason, "outside the reach of the caller: ") ||
		!strings.Contains(denied[0].Reason, "server:backup on the installation") {
		t.Fatalf("reason = %q, want the missing actions of the escalation", denied[0].Reason)
	}
}
