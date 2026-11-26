package dustpan

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestNewServerRefusesBuiltinStoreWithoutSettings proves that NewServer refuses
// a built-in store when the caller gave no WithSettings. The error names the
// fix: OpenStore, WithBuiltinStore and WithSettings.
func TestNewServerRefusesBuiltinStoreWithoutSettings(t *testing.T) {
	t.Parallel()

	store, openErr := OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cases := []struct {
		name   string
		option Option
	}{
		{name: "WithBuiltinStore", option: WithBuiltinStore(store)},
		{name: "WithSQLite", option: WithSQLite(filepath.Join(t.TempDir(), "dustpan.db"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, err := NewServer(tc.option)
			if err == nil {
				t.Fatalf("NewServer(%s) = %v, nil error, want an error", tc.name, srv)
			}

			checkBuiltinStoreRefusal(t, err)
		})
	}
}

// checkBuiltinStoreRefusal checks that err wraps ErrMissingOption and names
// each part of the fix.
func checkBuiltinStoreRefusal(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, ErrMissingOption) {
		t.Fatalf("NewServer error = %v, want ErrMissingOption", err)
	}

	for _, want := range []string{"OpenStore", "WithBuiltinStore", "WithSettings"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("NewServer error = %v, want it to name %s", err, want)
		}
	}
}

// TestInstallationReachWiring proves the wiring of the installation reach
// setting: a server built without a settings service refuses to build an
// access service, and a server built with one has an authorizer that reads the
// stored value. NewServer does not expose the authorizer, so the test builds
// the access service with the helper that NewServer uses.
func TestInstallationReachWiring(t *testing.T) {
	t.Parallel()

	// No settings service: newAccessService must fail.
	none := openReachTestStore(t, "no-settings.db")

	cfg := defaultSettings()
	cfg.namespaceStore = none
	cfg.settingsConfig = settingsConfig{store: nil, declarations: nil, static: nil}

	if svc, svcErr := newAccessService(cfg); svcErr == nil {
		t.Fatalf("newAccessService without settings = %v, nil error, want an error", svc)
	}

	// With a settings store: newAccessService builds the service and its
	// authorizer carries the settings port.
	st := openReachTestStore(t, "dustpan.db")

	cfg = defaultSettings()
	cfg.namespaceStore = st
	cfg.settingsConfig = settingsConfig{
		store:        st.SettingStore(),
		declarations: cli.SettingDeclarations(),
		static:       nil,
	}

	svc, err := newAccessService(cfg)
	if err != nil || svc == nil {
		t.Fatalf("newAccessService with settings = %v, %v, want a service", svc, err)
	}

	authz := svc.Authorizer()
	if authz == nil {
		t.Fatal("access service has no authorizer")
	}

	settings := newSettingService(cfg, cfg.namespaceStore)
	if settings == nil {
		t.Fatal("newSettingService with a store = nil, want a service")
	}

	// Store false through the settings service that the server wires, so the
	// authorizer reads the stored value and not only its default.
	storeReachValue(t, settings, "false")

	// A person with one grant on the installation: the viewer role holds
	// namespace:read, and the operator role holds an action of the
	// installation.
	createReachFixture(t, st, time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC))

	checkReachDecisions(t, authz)
}

// openReachTestStore opens a store in a temporary directory and closes it when
// the test ends.
func openReachTestStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), name))
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

// storeReachValue stores value as the reach setting through the settings
// service.
func storeReachValue(t *testing.T, settings *setting.Service, value string) {
	t.Helper()

	entry := audit.Entry{
		At: time.UnixMilli(1), Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "", Object: audit.ServerScope.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}

	patch := setting.Patch{{Key: access.SettingInstallationGrantsReachNamespaces, Value: value, Delete: false}}
	req := setting.PatchRequest{Entry: entry, IfMatch: 0, Fresh: true}

	if _, err := settings.Patch(t.Context(), setting.InstallationScope(), patch, req); err != nil {
		t.Fatalf("store the reach setting: %v", err)
	}
}

// checkReachDecisions checks that the authorizer reads the stored false: a
// grant on the installation does not reach a namespace, and it still gives the
// actions of the installation.
func checkReachDecisions(t *testing.T, authz *access.Authorizer) {
	t.Helper()

	holder := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "holder", DisplayName: "",
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	denied := authz.Authorize(t.Context(), holder, identity.ActionNamespaceRead, identity.NamespaceObject("acme"))
	if !errors.Is(denied, identity.ErrDenied) {
		t.Fatalf("Authorize namespace:read with the reach false = %v, want identity.ErrDenied", denied)
	}

	oerr := authz.Authorize(t.Context(), holder, identity.ActionServerReadSettings, identity.ServerObject())
	if oerr != nil {
		t.Fatalf("Authorize server:read-settings with the reach false = %v, want nil", oerr)
	}
}

// createReachFixture stores the namespace "acme", the person "holder" and the
// two grants on the installation that checkReachDecisions reads.
func createReachFixture(t *testing.T, st *sqlite.Store, at time.Time) {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, ID: "prn-holder", Subject: "holder", DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		BreakGlass: false, DisabledAt: time.Time{},
	}
	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, "principal:prn-holder")); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-reach-viewer", Role: access.RoleViewer,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-holder"},
	}

	fence, grantEntry := storetest.GrantWrite(t, st, identity.ServerObject(), at, "grant:"+grant.ID)
	if err := st.CreateGrant(t.Context(), grant, fence, grantEntry, nil); err != nil {
		t.Fatalf("create grant %s: %v", grant.ID, err)
	}

	grant = access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-reach-operator",
		Role: access.RoleInstallationOperator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-holder"},
	}

	fence, grantEntry = storetest.GrantWrite(t, st, identity.ServerObject(), at, "grant:"+grant.ID)
	if err := st.CreateGrant(t.Context(), grant, fence, grantEntry, nil); err != nil {
		t.Fatalf("create grant %s: %v", grant.ID, err)
	}
}
