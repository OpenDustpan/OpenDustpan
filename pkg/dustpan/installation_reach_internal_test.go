package dustpan

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
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
// setting through a server that NewServer builds: the value false, stored in
// the settings store of that server, makes the authorizer of the server deny a
// namespace action to a person whose only grants are on the installation. The
// manual lock service of the server checks each call with that authorizer.
func TestInstallationReachWiring(t *testing.T) {
	t.Parallel()

	w := newWiredSignIn(t, nil)

	cfg := defaultSettings()
	cfg.namespaceStore = w.store
	cfg.settingsConfig = settingsConfig{
		store:        w.store.SettingStore(),
		declarations: access.SettingDeclarations(),
		static:       nil,
	}

	settings := newSettingService(cfg, cfg.namespaceStore)
	if settings == nil {
		t.Fatal("newSettingService with a store = nil, want a service")
	}

	// Store false in the store that the server reads.
	storeReachValue(t, settings, "false")

	// A person with grants on the installation only: the viewer role holds
	// namespace:read.
	createReachFixture(t, w.store, time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC))

	locks := w.srv.ManualLocks()
	if locks == nil {
		t.Fatal("the server has no manual lock service")
	}

	holder := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "holder", DisplayName: "",
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	_, err := locks.Get(t.Context(), holder, manuallock.NamespaceTarget("acme"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("manual lock read in a namespace with the reach false = %v, want identity.ErrDenied", err)
	}
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

// createReachFixture stores the namespace "acme", the person "holder" and the
// two grants on the installation that TestInstallationReachWiring reads.
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
