package server_test

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in. The installation API
// serves no public route: every route needs a signed-in principal.
func publicPatterns() []string {
	return nil
}

// stubAuthenticator denies every token. Tests that send no token never reach it.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	return identity.Principal{}, identity.ErrUnauthenticated
}

// tokenAuthenticator signs in exactly the test token.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}, nil
}

// stubAuthorizer denies every action.
type stubAuthorizer struct{}

func (stubAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// selectiveAuthorizer denies exactly the actions in deny and allows the rest.
type selectiveAuthorizer struct {
	deny map[identity.Action]bool
}

func (s selectiveAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if s.deny[action] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// stubMetrics writes a fixed exposition. Tests that check the body use
// their own writer; this one only proves the route serves.
type stubMetrics struct{}

func (stubMetrics) Write(w io.Writer) error {
	_, err := io.WriteString(w, "# HELP dustpan_http_requests_total HTTP requests.\n")

	return err //nolint:wrapcheck // Transparent writer: callers must see the original error.
}

// stubNamespaceStore keeps no namespace: it exists only to build a namespace
// service for handler construction where no request needs it.
type stubNamespaceStore struct{}

func (stubNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error { return nil }

func (stubNamespaceStore) GetNamespace(_ context.Context, _ namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

func (stubNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (stubNamespaceStore) Ping(_ context.Context) error { return nil }

func newTestHandler(t *testing.T) *apiserver.Handler {
	t.Helper()

	return newServiceBackedHandler(t, stubAuthenticator{}, stubAuthorizer{}, runningSeed())
}

// newEnforcingHandler builds a Handler whose authorization the caller
// controls, so the enforcement tests can tell authorization apart from
// backend failures.
func newEnforcingHandler(t *testing.T, authz identity.Authorizer) *apiserver.Handler {
	t.Helper()

	return newServiceBackedHandler(t, tokenAuthenticator{}, authz, runningSeed())
}

// testBackupID is the running backup the handler builders seed, so the
// GET-by-id route answers 200 in the generic enforcement tests.
const testBackupID = backup.ID("bkp-test00000000000001")

// testNamespace is the namespace the override switch routes name. The
// service-backed handler seeds it, so those routes answer 200 when allowed.
const testNamespace = "routes"

// testRestoreID is the failed restore the handler builders seed, so the
// GET-by-id route answers 200 in the generic enforcement tests. The seed is
// failed, never checking or applying, so the POST route answers 202 in
// the same tests instead of 409 for a running restore, and GET skips the
// manifest read that only succeeded rows need.
const testRestoreID = backup.RestoreID("rst-test00000000000001")

// runningSeed returns a running backup row: the state a killed backup leaves,
// which the next server start marks failed.
func runningSeed() backup.Record {
	return backup.Record{
		StartedAt: time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		EndedAt:   time.Time{},
		ID:        testBackupID,
		Target:    "dir:test",
		Error:     "",
		State:     backup.StateRunning,
	}
}

// newServiceBackedHandler builds a Handler with a real SQLite-backed backup
// service over fresh stores, seeded with seeds. The registry starts with the
// seeds, so tests that need rows pass them and tests that need an empty
// registry pass none.
func newServiceBackedHandler(
	t *testing.T,
	authn identity.Authenticator,
	authz identity.Authorizer,
	seeds ...backup.Record,
) *apiserver.Handler {
	t.Helper()

	st := openRouteTestStore(t)
	live := openRouteTestBlobs(t)
	target := openRouteTestBlobs(t)
	service := newRouteTestBackups(t, st, live, target)
	restores := newRouteTestRestores(t, st, service, live, target)
	seedRouteTestRows(t, st, seeds)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, seedErr := namespaces.Create(t.Context(), testNamespace); seedErr != nil {
		t.Fatalf("seed namespace: %v", seedErr)
	}

	allow, err := ipallow.NewService(ipallow.Config{Store: st, Namespaces: namespaces, Now: nil, Enabled: true})
	if err != nil {
		t.Fatalf("ip allowlist service: %v", err)
	}

	sessionPolicies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policy service: %v", err)
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  authn,
		Authorizer:     authz,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        service,
		Restores:       restores,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         st,
		Principals:     seedRoutePrincipals(t, st),
		Tokens:         seedRouteTokens(t, st),
		Access:         seedRouteAccess(t, st),
		Audit:          st,
		OnAuditFailure: nil,
		DeletedUnits:   stubSweeper{},
		IPAllowlists:   allow,
		SessionPolicy:  sessionPolicies,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// Principal, group and token IDs that the handler builders seed, so the
// routes with an ID in the path answer their success status in the generic
// enforcement tests. testPrincipalID is a member of testGroupID, and
// testOtherPrincipalID is in no group, so the member add answers 201 and the
// member remove 204. testTokenID is a personal token of testPrincipalID,
// testServiceTokenID a service token of testOtherPrincipalID, and
// testOwnTokenID a personal token of the test caller, whose subject is
// tester.
const (
	testPrincipalID      = "prn-routes0000000000001"
	testOtherPrincipalID = "prn-routes0000000000002"
	testCallerID         = "prn-routes0000000000003"
	// testNamespacePrincipalID is a service principal of testNamespace.
	testNamespacePrincipalID = "prn-routes0000000000004"
	testGroupID              = "grp-routes0000000000001"
	testTokenID              = "tok-routes0000000000001"
	testServiceTokenID       = "tok-routes0000000000002"
	testOwnTokenID           = "tok-routes0000000000003"
	testRoleID               = "rol-routes0000000000001"
)

// allowGrants passes every escalation check, so the routes that create or
// rotate a token for another principal answer their success status.
type allowGrants struct{}

func (allowGrants) RequireHeld(context.Context, identity.Principal, string) error { return nil }

// allowAll allows every action, so the token service passes its own check
// of principal:manage-tokens and the route alone decides.
type allowAll struct{}

func (allowAll) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

// seedRouteTokens writes the person record of the test caller and one
// token of each kind, so the token routes with an ID answer their success
// status, and returns the service over st. seedRoutePrincipals runs first.
func seedRouteTokens(t *testing.T, st *sqlite.Store) *accesstoken.Service {
	t.Helper()

	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Millisecond)

	caller := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          testCallerID,
		Subject:     "tester",
		DisplayName: "Tester",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}
	if err := st.CreatePrincipal(ctx, caller, storetest.ServerChange(at, principal.Ref(testCallerID))); err != nil {
		t.Fatalf("seed caller: %v", err)
	}

	seeds := []struct {
		id, principalID string
		kind            accesstoken.Kind
	}{
		{testTokenID, testPrincipalID, accesstoken.KindPersonal},
		{testServiceTokenID, testOtherPrincipalID, accesstoken.KindService},
		{testOwnTokenID, testCallerID, accesstoken.KindPersonal},
	}
	for i, seed := range seeds {
		tok := accesstoken.Token{
			CreatedAt:   at.Add(-2 * time.Hour),
			ExpiresAt:   at.Add(48 * time.Hour),
			LastUsedAt:  time.Time{},
			RevokedAt:   time.Time{},
			DisabledAt:  time.Time{},
			ID:          seed.id,
			PrincipalID: seed.principalID,
			Name:        "seed",
			Kind:        seed.kind,
			ReplacedBy:  "",
			CreatedBy:   "admin",
		}

		d := accesstoken.Digest{byte(i + 1)}
		if err := st.CreateToken(ctx, tok, d, storetest.ServerChange(at, accesstoken.Ref(seed.id))); err != nil {
			t.Fatalf("seed token %s: %v", seed.id, err)
		}
	}

	// Disable the person testPrincipalID, so the enable route answers 200:
	// it and its token were made more than one hour before the sweep.
	sweep := accesstoken.Sweep{Inactivity: time.Hour, Tokens: false, Accounts: true}
	if _, sweepErr := st.SweepInactive(ctx, sweep, storetest.ServerChange); sweepErr != nil {
		t.Fatalf("seed sweep: %v", sweepErr)
	}

	svc, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, Freshness: nil, Grants: allowGrants{}, Authorizer: allowAll{}, Logger: nil, Now: nil,
		NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return svc
}

// stubCollections names the collection testCollection with its stable ID,
// so that a grant on it is accepted (ADR 51). No unit is in a collection here.
type stubCollections struct{}

func (stubCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

func (stubCollections) CollectionID(_ context.Context, obj identity.Object) (string, bool, error) {
	if obj.CollectionName() == testCollection {
		return "col-" + testCollection, true, nil
	}

	return "", false, nil
}

// Grant IDs that seedRouteAccess writes, one on each object kind, so each
// revoke route answers 204 in the generic enforcement tests.
const (
	testServerGrantID     = "grt-routes-server"
	testNamespaceGrantID  = "grt-routes-namespace"
	testCollectionGrantID = "grt-routes-collection"
	testUnitGrantID       = "grt-routes-unit"
	testPrincipalGrantID  = "grt-routes-principal"
)

// testCollection and testUnitName name the collection and the unit of the
// seeded grants.
const (
	testCollection = "payments"
	testUnitName   = "web/prod"
)

// seedRouteAccess gives the test caller (seedRouteTokens writes its
// record) the site administrator role on the installation, so the grant routes pass the
// escalation check, a custom role that no grant holds, and one grant on each
// object kind. It returns the access service over st.
func seedRouteAccess(t *testing.T, st *sqlite.Store) *access.Service {
	t.Helper()

	ctx := t.Context()
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	rol := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: testRoleID, Name: "deployer", Description: "",
		Actions: []identity.Action{identity.ActionUnitRead}, BuiltIn: false,
	}
	if err := st.CreateRole(ctx, rol, storetest.ServerChange(at, "role:"+rol.ID)); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	unit := seedStack(t, st, testNamespace, "web", "prod")

	other := access.Subject{Kind: access.SubjectPrincipal, ID: testOtherPrincipalID}

	grants := []access.Grant{
		{
			CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-routes-tester",
			Role: access.RoleSiteAdministrator, CreatedBy: "admin",
			Subject: access.Subject{Kind: access.SubjectPrincipal, ID: testCallerID},
		},
		{
			CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: testServerGrantID,
			Role: access.RoleViewer, CreatedBy: "admin", Subject: other,
		},
		{
			CreatedAt: at, Condition: nil, Object: identity.NamespaceObject(testNamespace), ID: testNamespaceGrantID,
			Role: access.RoleViewer, CreatedBy: "admin", Subject: other,
		},
		{
			CreatedAt: at,
			Condition: nil,
			Object: identity.CollectionObject(testNamespace, testCollection).
				WithCollectionID("col-" + testCollection),
			ID:        testCollectionGrantID,
			Role:      access.RoleViewer,
			CreatedBy: "admin",
			Subject:   other,
		},
		{
			CreatedAt: at, Condition: nil, Object: unit, ID: testUnitGrantID,
			Role: access.RoleViewer, CreatedBy: "admin", Subject: other,
		},
		{
			CreatedAt: at, Condition: nil, Object: identity.PrincipalObject(testOtherPrincipalID),
			ID: testPrincipalGrantID, Role: access.RoleTokenManager, CreatedBy: "admin",
			Subject: access.Subject{Kind: access.SubjectPrincipal, ID: testPrincipalID},
		},
	}
	for i := range grants {
		fence, e := storetest.GrantWrite(t, st, grants[i].Object, at, "grant:"+grants[i].ID)

		seedErr := st.CreateGrant(ctx, grants[i], fence, e)
		if seedErr != nil {
			t.Fatalf("seed grant %q: %v", grants[i].ID, seedErr)
		}
	}

	authz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: st, Callers: st, Collections: stubCollections{}, Units: st, Principals: st},
	)
	if err != nil {
		t.Fatalf("access authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz, Now: nil, NewID: nil,
		BootstrapAdmins: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	return svc
}

// seedRoutePrincipals writes the principals, the group and the membership
// that the principal and group routes name, and returns the service over st.
// seedNamespacePrincipal stores testNamespacePrincipalID, a service
// principal of testNamespace.
func seedNamespacePrincipal(t *testing.T, st *sqlite.Store, at time.Time) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: testNamespacePrincipalID, Subject: "ci-routes",
		DisplayName: "CI routes", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: testNamespace, GroupCount: 0, BreakGlass: false,
	}

	fence, e := storetest.GrantWrite(t, st, identity.NamespaceObject(testNamespace), at, principal.Ref(p.ID))
	if err := st.CreateNamespacePrincipal(t.Context(), p, fence, e); err != nil {
		t.Fatalf("seed namespace principal: %v", err)
	}
}

func seedRoutePrincipals(t *testing.T, st *sqlite.Store) *principal.Service {
	t.Helper()

	ctx := t.Context()
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	seeds := []principal.Principal{
		{
			CreatedAt:   at,
			UpdatedAt:   at,
			DisabledAt:  time.Time{},
			ID:          testPrincipalID,
			Subject:     "alice",
			DisplayName: "Alice",
			Kind:        principal.KindPerson,
			Source:      principal.SourceLocal,
			Namespace:   "",
			GroupCount:  0, BreakGlass: false,
		},
		{
			CreatedAt:   at,
			UpdatedAt:   at,
			DisabledAt:  time.Time{},
			ID:          testOtherPrincipalID,
			Subject:     "ci-deploy",
			DisplayName: "CI deploy",
			Kind:        principal.KindService,
			Source:      principal.SourceLocal,
			Namespace:   "",
			GroupCount:  0, BreakGlass: false,
		},
	}
	for i := range seeds {
		if err := st.CreatePrincipal(ctx, seeds[i], storetest.ServerChange(at, "principal:"+seeds[i].ID)); err != nil {
			t.Fatalf("seed principal: %v", err)
		}
	}

	group := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: testGroupID, Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0,
	}
	if err := st.CreateGroup(ctx, group, storetest.ServerChange(at, "group:"+testGroupID)); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	member := principal.Membership{AddedAt: at, GroupID: testGroupID, PrincipalID: testPrincipalID}
	joined := storetest.ServerChange(at, "group:"+testGroupID+"#member:"+testPrincipalID)
	if err := st.AddMember(ctx, member, joined); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	seedNamespacePrincipal(t, st, at)

	svc, err := principal.NewService(principal.Config{Store: st, Guard: nil, Namespaces: st, Now: nil, NewID: nil})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	return svc
}

// routeBodies holds the body of each route that needs one in the generic
// enforcement tests, by method and path.
//
//nolint:gochecknoglobals,gosec // A constant table of request bodies; G101 reads "token" in the paths.
var routeBodies = map[string]string{
	"PUT /api/server/namespaces/{namespace}/override-switches": `{"allowForcedWrites":false,"allowUnlockedWrites":false}`,
	"PUT /api/server/namespaces/{namespace}/ip-allowlist":      `{"cidrs":["10.0.0.0/8"]}`,
	"POST /api/server/namespaces":                              `{"name":"acme"}`,
	"POST /api/server/restores": `{"backupID":"` + string(
		testBackupID,
	) + `"}`,
	"POST /api/server/principals":                                             `{"subject":"bob","kind":"person"}`,
	"PATCH /api/server/principals/{id}":                                       `{"displayName":"Alice A."}`,
	"POST /api/server/groups":                                                 `{"name":"dev"}`,
	"PATCH /api/server/groups/{id}":                                           `{"description":"Runs the platform"}`,
	"POST /api/server/groups/{id}/members":                                    `{"principalID":"` + testOtherPrincipalID + `"}`,
	"POST /api/server/roles":                                                  `{"name":"releaser","actions":["unit:read"]}`,
	"PATCH /api/server/roles/{id}":                                            `{"description":"Reads units"}`,
	"POST /api/server/grants":                                                 routeGrantBody,
	"POST /api/server/namespaces/{namespace}/grants":                          routeGrantBody,
	"POST /api/server/namespaces/{namespace}/collections/{collection}/grants": routeGrantBody,
	"POST /api/server/namespaces/{namespace}/unit-grants":                     routeGrantBody,
	"POST /api/server/tokens":                                                 `{"principalID":"` + testPrincipalID + `","name":"ci"}`,
	"POST /api/server/tokens/{id}/rotate":                                     `{}`,
	"POST /api/server/service-tokens":                                         `{"principalID":"` + testOtherPrincipalID + `","name":"ci"}`,
	"POST /api/server/service-tokens/{id}/rotate":                             `{}`,
	"POST /api/server/caller/tokens":                                          `{"name":"laptop"}`,
	"POST /api/server/namespaces/{namespace}/principals":                      `{"subject":"ci-new"}`,
	"PATCH /api/server/namespaces/{namespace}/principals/{id}":                `{"displayName":"Routes bot"}`,
	"POST /api/server/principals/{principal}/tokens":                          `{"name":"ci"}`,
	"POST /api/server/principals/{principal}/tokens/{id}/rotate":              `{}`,
	"POST /api/server/principals/{principal}/grants":                          routePrincipalGrantBody,
	"POST /api/server/caller/tokens/{id}/rotate":                              `{}`,
	"PATCH /api/server/token-policy":                                          `{"inactivitySeconds":0}`,
	"PATCH /api/server/session-policy":                                        `{"idleSeconds":900}`,
}

// routeGrantBody is the body of each grant route: a role that no seeded
// grant gives to the group.
const routeGrantBody = `{"subject":"group:` + testGroupID + `#member","role":"writer"}`

// routePrincipalGrantBody is the body of the grant route of a principal: a
// role with principal actions only, which no seeded grant gives to the
// group.
const routePrincipalGrantBody = `{"subject":"group:` + testGroupID + `#member","role":"token-manager"}`

// openRouteTestStore opens a fresh SQLite store for one route test.
func openRouteTestStore(t *testing.T) *sqlite.Store {
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

	return st
}

// openRouteTestBlobs opens a fresh file blob store for one route test.
func openRouteTestBlobs(t *testing.T) *blobfs.Store {
	t.Helper()

	live, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open live store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live store: %v", closeErr)
		}
	})

	return live
}

// newRouteTestBackups builds the backup service of one route test.
func newRouteTestBackups(
	t *testing.T, st *sqlite.Store, live, target *blobfs.Store,
) *backup.Service {
	t.Helper()

	service, err := backup.NewService(backup.Config{
		Exporter:    st,
		Registry:    st,
		Blobs:       live,
		StateOpener: nil,
		Target:      target,
		TargetName:  "dir:test",
		Now:         nil,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	return service
}

// newRouteTestRestores builds the restore service of one route test.
func newRouteTestRestores(
	t *testing.T, st *sqlite.Store, service *backup.Service, live, target *blobfs.Store,
) *backup.RestoreService {
	t.Helper()

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:        st,
		Registry:    st,
		Backups:     service,
		Maintenance: newFakeMaintenance(maintenance.StateNormal),
		Blobs:       live,
		Target:      target,
		Key:         nil,
		Region:      "test",
		AfterApply:  nil,
		Now:         nil,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return restores
}

// seedRouteTestRows seeds the backup seeds and one failed restore, so the
// GET-by-id routes answer 200 in the generic enforcement tests.
func seedRouteTestRows(t *testing.T, st *sqlite.Store, seeds []backup.Record) {
	t.Helper()

	for _, seed := range seeds {
		if seedErr := st.CreateBackup(t.Context(), seed); seedErr != nil {
			t.Fatalf("seed backup %s: %v", seed.ID, seedErr)
		}
	}

	seed := backup.RestoreRecord{
		StartedAt:   time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		EndedAt:     time.Date(2026, time.October, 11, 13, 0, 0, 0, time.UTC),
		ID:          testRestoreID,
		BackupID:    testBackupID,
		PreBackupID: "",
		RequestedBy: "tester",
		Error:       "",
		State:       backup.RestoreFailed,
	}

	seedErr := st.CreateRestore(t.Context(), seed)
	if seedErr != nil {
		t.Fatalf("seed restore %s: %v", testRestoreID, seedErr)
	}
}

// TestRoutesDeclareActions walks every registered route and fails for a
// route with no declared action. It checks both directions: every served
// pattern has a table row that passes the scope check, and every table row
// is served. A row without actions or a resolver still fails.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	public := map[string]bool{}
	for _, pattern := range publicPatterns() {
		public[pattern] = true
	}

	byPattern := map[string]apiserver.Route{}
	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteScope(t, route, public)
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for _, route := range byPattern {
		probeRoute(t, handler, route)
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: it
// fails if any production file in this package builds a ServeMux or touches
// the inner mux of patternMux outside routes.go, where Handle and HandleFunc
// both record. Test files are skipped; they never serve production routes.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatalf("read file %s: %v", name, err)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// checkRouteScope fails for a route that is neither public nor authorized
// with actions and a resolver.
func checkRouteScope(t *testing.T, route apiserver.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	switch route.Scope {
	case apiserver.ScopePublic:
		checkPublicRoute(t, route, public)
	case apiserver.ScopeAuthorized:
		checkAuthorizedRoute(t, route, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}
}

// checkPublicRoute fails for a public route that is not on the public list
// or that declares actions or a resolver.
func checkPublicRoute(t *testing.T, route apiserver.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if !public[pattern] {
		t.Fatalf("route %q is public but not on the public list", pattern)
	}

	if len(route.Actions) != 0 {
		t.Fatalf("public route %q declares actions", pattern)
	}

	if route.Resolve != nil {
		t.Fatalf("public route %q declares a resolver", pattern)
	}
}

// checkAuthorizedRoute fails for an authorized route that is on the public
// list or that declares no action or no resolver.
func checkAuthorizedRoute(t *testing.T, route apiserver.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if public[pattern] {
		t.Fatalf("route %q is on the public list but declares actions", pattern)
	}

	if len(route.Actions) == 0 {
		t.Fatalf("route %q declares no action", pattern)
	}

	if route.Resolve == nil {
		t.Fatalf("route %q declares actions but no resolver", pattern)
	}
}

// probeRoute checks that a declared route is registered: a request without a
// token must fail closed, never with 404.
func probeRoute(t *testing.T, handler *apiserver.Handler, route apiserver.Route) {
	t.Helper()

	req := routeRequest(t, route, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", route.Method+" "+route.Path)
	}
}

// routeRequest builds a valid request for route: the {id} placeholder holds
// the seeded running backup or the seeded checking restore, so the GET-by-id
// routes answer 200 when allowed, and the namespace create route carries a
// name, so it answers 201 when allowed. token is sent as the Authorization
// Bearer header when not empty.
func routeRequest(t *testing.T, route apiserver.Route, token string) *http.Request {
	t.Helper()

	id := string(testBackupID)
	owner := routePrincipalIDs(route.Path)

	switch {
	case owner.ofOne:
		id = owner.id
	case strings.HasPrefix(route.Path, "/api/server/restores"):
		id = string(testRestoreID)
	case strings.HasPrefix(route.Path, "/api/server/principals"):
		id = testPrincipalID
	case strings.HasPrefix(route.Path, "/api/server/groups"):
		id = testGroupID
	case strings.HasPrefix(route.Path, "/api/server/roles"):
		id = testRoleID
	case strings.HasPrefix(route.Path, "/api/server/tokens"):
		id = testTokenID
	case strings.HasPrefix(route.Path, "/api/server/service-tokens"):
		id = testServiceTokenID
	case strings.HasPrefix(route.Path, "/api/server/caller/tokens"):
		id = testOwnTokenID
	default:
		id = routeGrantID(route.Path, id)
	}

	path := strings.ReplaceAll(route.Path, "{id}", id)
	path = strings.ReplaceAll(path, "{namespace}", testNamespace)
	path = strings.ReplaceAll(path, "{principal}", owner.principal)
	path = strings.ReplaceAll(path, "{collection}", testCollection)

	path += routeQuery(route.Path)

	var body io.Reader = http.NoBody
	if text, ok := routeBodies[route.Method+" "+route.Path]; ok {
		body = strings.NewReader(text)
	}

	req := httptest.NewRequestWithContext(t.Context(), route.Method, path, body)

	// The resolvers and the handlers read the path values, which the mux
	// sets in production.
	req.SetPathValue("namespace", testNamespace)
	req.SetPathValue("id", id)
	req.SetPathValue("principal", owner.principal)
	req.SetPathValue("collection", testCollection)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

// routeOwner is the {principal} of a route, and for a route of one
// principal the {id} of its seeded grant or token.
type routeOwner struct {
	principal string
	id        string
	ofOne     bool
}

// routePrincipalIDs returns the owner of a route of one principal: the
// service principal testOtherPrincipalID, with its seeded grant or token,
// or, on the routes of the principals of a namespace, the seeded principal
// of testNamespace. Any other path answers testPrincipalID.
func routePrincipalIDs(path string) routeOwner {
	if strings.HasPrefix(path, "/api/server/namespaces/{namespace}/principals") {
		return routeOwner{principal: testPrincipalID, id: testNamespacePrincipalID, ofOne: true}
	}

	if !strings.HasPrefix(path, "/api/server/principals/{principal}/") {
		return routeOwner{principal: testPrincipalID, id: "", ofOne: false}
	}

	if strings.Contains(path, "/grants") {
		return routeOwner{principal: testOtherPrincipalID, id: testPrincipalGrantID, ofOne: true}
	}

	return routeOwner{principal: testOtherPrincipalID, id: testServiceTokenID, ofOne: true}
}

// routeQuery returns the query that a route with the path pattern path
// needs: the unit of a unit route, or the object of the caller access.
func routeQuery(path string) string {
	switch {
	case strings.Contains(path, "/unit-grants") || strings.Contains(path, "/unit-access-review"):
		return "?tool=pulumi&unit=" + url.QueryEscape(testUnitName)
	case path == "/api/server/caller/access":
		return "?object=" + testNamespace
	default:
		return ""
	}
}

// routeGrantID returns the seeded grant on the object kind of a grant path,
// or id for any other path.
func routeGrantID(path, id string) string {
	switch {
	case strings.Contains(path, "/unit-grants"):
		return testUnitGrantID
	case strings.Contains(path, "/collections/"):
		return testCollectionGrantID
	case strings.HasPrefix(path, "/api/server/namespaces/") && strings.Contains(path, "/grants"):
		return testNamespaceGrantID
	case strings.HasPrefix(path, "/api/server/grants"):
		return testServerGrantID
	default:
		return id
	}
}

// createsRecord reports whether a POST to path creates a record and
// answers 201: a namespace, a principal, a group, a member, a role, a
// grant, or a token (a rotation makes a new one).
func createsRecord(path string) bool {
	created := []string{
		"/api/server/namespaces", "/api/server/principals", "/api/server/groups",
		"/api/server/groups/{id}/members", "/api/server/roles", "/api/server/namespaces/{namespace}/principals",
	}

	return slices.Contains(created, path) ||
		strings.HasSuffix(path, "/grants") || strings.HasSuffix(path, "/unit-grants") ||
		strings.HasSuffix(path, "tokens") || strings.HasSuffix(path, "/rotate")
}

// wantSuccessStatus answers the success status of route: POST
// /api/server/backups and POST /api/server/restores start work in the
// background and answer 202, POST /api/server/namespaces creates a namespace
// and answers 201, every other route answers 200.
func wantSuccessStatus(route apiserver.Route) int {
	if route.Method == http.MethodPost &&
		(route.Path == "/api/server/backups" || route.Path == "/api/server/restores") {
		return http.StatusAccepted
	}

	if route.Method == http.MethodGet &&
		route.Path == "/api/server/namespaces/{namespace}/collections/{collection}/access-review" {
		// The export of a collection waits for the units inside it (ADR 52).
		return http.StatusNotImplemented
	}

	if route.Method == http.MethodPost && createsRecord(route.Path) {
		return http.StatusCreated
	}

	if route.Method == http.MethodDelete {
		return http.StatusNoContent
	}

	return http.StatusOK
}

// TestRoutesEnforceAuthorization proves the table is enforced: with an
// Authorizer that denies a declared action the route answers 403, and it
// answers its success status when allowed. Table-driven over all routes.
func TestRoutesEnforceAuthorization(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteEnforcement(t, route)
		})
	}
}

// checkRouteEnforcement proves one route enforces its row: it answers its
// success status when allowed, and an authorized route answers 403 when its
// actions are denied.
func checkRouteEnforcement(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope == apiserver.ScopePublic {
		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
		}

		return
	}

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if want := wantSuccessStatus(route); rec.Code != want {
		t.Fatalf("allowed request answers %d, want %d (%s)", rec.Code, want, rec.Body.String())
	}

	deny := map[identity.Action]bool{}
	for _, action := range route.Actions {
		deny[action] = true
	}

	denied := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// recordedCall is one Authorize call: the action on the object wire form.
type recordedCall struct {
	action identity.Action
	object string
}

// recordingAuthorizer allows every action and records every call, so tests
// prove which object a route authorizes.
type recordingAuthorizer struct {
	calls []recordedCall
}

func (r *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	r.calls = append(r.calls, recordedCall{action: action, object: obj.String()})

	return nil
}

// denyObjectAuthorizer denies the one object wire form in deny and allows
// everything else, so tests prove a route authorizes the resolved object:
// denying it answers 403.
type denyObjectAuthorizer struct {
	deny string
}

func (d denyObjectAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if obj.String() == d.deny {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// TestRoutesAuthorizeDeclaredObject proves every authorized route asks for
// its declared actions on the object its resolver returns: the recording
// Authorizer sees each declared action on the resolved object, and denying
// that object answers 403. A row that authorizes another object still fails.
func TestRoutesAuthorizeDeclaredObject(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteObject(t, route)
		})
	}
}

// checkRouteObject proves one route authorizes its declared actions on its
// resolved object.
func checkRouteObject(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope == apiserver.ScopePublic {
		checkPublicRouteObject(t, route)

		return
	}

	checkAuthorizedRouteObject(t, route)
}

// checkPublicRouteObject proves a public route serves without sign-in and
// never calls the Authorizer.
func checkPublicRouteObject(t *testing.T, route apiserver.Route) {
	t.Helper()

	recorder := &recordingAuthorizer{calls: nil}
	allowed := newEnforcingHandler(t, recorder)

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
	}

	if len(recorder.calls) != 0 {
		t.Fatalf("public route calls %v, want no Authorizer call", recorder.calls)
	}
}

// checkAuthorizedRouteObject proves an authorized route asks for its declared
// actions on its resolved object: the recorder sees each action on it, and
// denying it answers 403.
func checkAuthorizedRouteObject(t *testing.T, route apiserver.Route) {
	t.Helper()

	rec := httptest.NewRecorder()

	expected, err := route.Resolve(rec, routeRequest(t, route, "test-token"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	recorder := &recordingAuthorizer{calls: nil}
	allowed := newEnforcingHandler(t, recorder)

	rec = httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if want := wantSuccessStatus(route); rec.Code != want {
		t.Fatalf("allowed request answers %d, want %d (%s)", rec.Code, want, rec.Body.String())
	}

	for _, action := range route.ActionsOn(expected) {
		if !containsCall(recorder.calls, recordedCall{action: action, object: expected.String()}) {
			t.Fatalf("route never asks %s on %q (calls %v)", action, expected.String(), recorder.calls)
		}
	}

	denied := newEnforcingHandler(t, denyObjectAuthorizer{deny: expected.String()})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("request denied on %q answers %d, want %d", expected.String(), rec.Code, http.StatusForbidden)
	}
}

// containsCall reports whether calls holds want.
func containsCall(calls []recordedCall, want recordedCall) bool {
	return slices.Contains(calls, want)
}

// TestUnknownPathAnswersJSON404 proves a path the table does not hold answers
// 404 as a JSON error from this adapter, so callers can tell it reached the
// installation API.
func TestUnknownPathAnswersJSON404(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/no-such-route", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want %q", got, "application/json")
	}

	if !strings.Contains(rec.Body.String(), `"code":404`) {
		t.Fatalf("unknown path body %s, want a JSON error with code 404", rec.Body.String())
	}
}

// TestNewHandlerRequiresDependencies proves NewHandler fails without each
// dependency.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	t.Parallel()

	namespaces, err := namespace.NewService(namespace.Config{Store: stubNamespaceStore{}, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	full := apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
	}

	cases := []struct {
		name string
		drop func(*apiserver.Config)
	}{
		{name: "authenticator", drop: func(c *apiserver.Config) { c.Authenticator = nil }},
		{name: "authorizer", drop: func(c *apiserver.Config) { c.Authorizer = nil }},
		{name: "logger", drop: func(c *apiserver.Config) { c.Logger = nil }},
		{name: "namespaces", drop: func(c *apiserver.Config) { c.Namespaces = nil }},
		{name: "maintenance", drop: func(c *apiserver.Config) { c.Maintenance = nil }},
		{name: "metrics", drop: func(c *apiserver.Config) { c.Metrics = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			tc.drop(&cfg)

			if _, err := apiserver.NewHandler(cfg); err == nil {
				t.Fatalf("NewHandler without %s succeeded", tc.name)
			}
		})
	}
}

// stubBackupExporter answers an empty export, for the handler backup test.
type stubBackupExporter struct{}

func (stubBackupExporter) ExportPortable(
	_ context.Context, _ backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	var none backup.Summary

	return none, nil, nil
}

// stubBackupRegistry answers no rows, for the handler backup test.
type stubBackupRegistry struct{}

func (stubBackupRegistry) CreateBackup(_ context.Context, _ backup.Record) error { return nil }

func (stubBackupRegistry) FinishBackup(
	_ context.Context, _ backup.ID, _ backup.State, _ string, _ time.Time,
) (backup.Record, error) {
	return backup.Record{
		StartedAt: time.Time{},
		EndedAt:   time.Time{},
		ID:        "",
		Target:    "",
		Error:     "",
		State:     "",
	}, nil
}

func (stubBackupRegistry) GetBackup(_ context.Context, _ backup.ID) (backup.Record, error) {
	return backup.Record{}, backup.ErrNotFound
}

func (stubBackupRegistry) ListBackups(_ context.Context) ([]backup.Record, error) { return nil, nil }

func (stubBackupRegistry) FailRunningBackups(_ context.Context, _ time.Time, _ string) error {
	return nil
}

// TestNewHandlerKeepsBackupService proves the backup service passes through
// Config untouched: the handler keeps it, and nil stays nil. The target never
// comes from a request: no handler field holds it.
func TestNewHandlerKeepsBackupService(t *testing.T) {
	t.Parallel()

	live, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open live store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live store: %v", closeErr)
		}
	})

	target, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	service, err := backup.NewService(backup.Config{
		Exporter:    stubBackupExporter{},
		Registry:    stubBackupRegistry{},
		Blobs:       live,
		StateOpener: nil,
		Target:      target,
		TargetName:  "dir:/var/backups",
		Now:         nil,
		Version:     "",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: stubNamespaceStore{}, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  stubAuthenticator{},
		Authorizer:     stubAuthorizer{},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        service,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if handler.Backups() != service {
		t.Fatal("handler keeps another backup service, want the configured one")
	}

	without, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  stubAuthenticator{},
		Authorizer:     stubAuthorizer{},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
	})
	if err != nil {
		t.Fatalf("handler without a target: %v", err)
	}

	if without.Backups() != nil {
		t.Fatal("handler without a backup target builds a backup service, want nil")
	}
}

// stubSweeper is an apiserver.DeletedUnitSweeper that purges nothing.
type stubSweeper struct{}

func (stubSweeper) Sweep(context.Context, audit.Entry) (inventory.SweepResult, error) {
	return inventory.SweepResult{
		Purged: []inventory.DeletedEntry{}, Failed: 0, Skipped: 0,
		FinishedBlobPurges: 0, PendingBlobPurges: 0,
	}, nil
}
