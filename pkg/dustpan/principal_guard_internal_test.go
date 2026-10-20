package dustpan

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestPrincipalServiceWithoutGrantsRefusesANonAdministrator proves the wiring:
// when the installation keeps no grants, the principal service still has a
// guard, and that guard refuses a caller who is not an administrator.
func TestPrincipalServiceWithoutGrantsRefusesANonAdministrator(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.namespaceStore = st

	svc, err := newPrincipalService(cfg, nil)
	if err != nil || svc == nil {
		t.Fatalf("newPrincipalService = %v, %v, want a service", svc, err)
	}

	var nonAdmin identity.Principal

	for name, run := range map[string]func() error{
		"delete":       func() error { return svc.DeletePrincipal(t.Context(), adminEntry(), nonAdmin, "p") },
		"enable":       func() error { return svc.EnablePrincipal(t.Context(), adminEntry(), nonAdmin, "p") },
		"disable":      func() error { return svc.DisablePrincipal(t.Context(), adminEntry(), nonAdmin, "p") },
		"delete group": func() error { return svc.DeleteGroup(t.Context(), adminEntry(), nonAdmin, "g") },
	} {
		if err := run(); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("%s by a non-administrator = %v, want identity.ErrDenied", name, err)
		}
	}
}
