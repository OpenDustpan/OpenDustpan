package dustpan

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/store/sqlite"
)

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
