package dustpan

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/store/sqlite"
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
// access service, and a server built with one has an authorizer that reads
// the stored value.
func TestInstallationReachWiring(t *testing.T) {
	t.Parallel()

	// No settings service: newAccessService must fail.
	st2, openErr := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan2.db"))
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}
	t.Cleanup(func() {
		if closeErr := st2.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.namespaceStore = st2
	cfg.settingsConfig = settingsConfig{store: nil, declarations: nil, static: nil}

	if svc, svcErr := newAccessService(cfg); svcErr == nil {
		t.Fatalf("newAccessService without settings = %v, nil error, want an error", svc)
	}

	// With a settings store: newAccessService builds the service and its
	// authorizer carries the settings port.
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

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

	// The authorizer reads the default true when nothing is stored, so a
	// grant on the installation reaches a namespace.
	// No grant is needed for the port check: a nil-equivalent read keeps
	// the default. The port is present, so GrantsReachNamespaces does not
	// error.
	reaches, rerr := authz.GrantsReachNamespaces(t.Context())
	if rerr != nil {
		t.Fatalf("GrantsReachNamespaces = %v, %v, want no error", reaches, rerr)
	}

	if !reaches {
		t.Fatalf("GrantsReachNamespaces with no stored value = %v, want true", reaches)
	}
}
