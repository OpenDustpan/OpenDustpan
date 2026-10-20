package dustpan

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
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

// TestPrincipalServiceWithGrantsUsesTheAccessService proves the other branch
// of the wiring: a non-nil access service becomes the guard. A principal
// without grants has nothing to take away, so the grants guard lets a
// non-administrator delete it, where the admin-only guard refuses.
func TestPrincipalServiceWithGrantsUsesTheAccessService(t *testing.T) {
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

	grants, err := newAccessService(cfg)
	if err != nil || grants == nil {
		t.Fatalf("newAccessService = %v, %v, want a service", grants, err)
	}

	svc, err := newPrincipalService(cfg, grants)
	if err != nil || svc == nil {
		t.Fatalf("newPrincipalService = %v, %v, want a service", svc, err)
	}

	created, err := svc.CreatePrincipal(t.Context(), adminEntry(),
		principal.Input{Subject: "alice", DisplayName: "", Kind: principal.KindPerson})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	var nonAdmin identity.Principal

	if err := svc.DeletePrincipal(t.Context(), adminEntry(), nonAdmin, created.ID); errors.Is(err, identity.ErrDenied) {
		t.Fatalf("delete of a principal without grants = %v, want the grants guard to allow it", err)
	}
}
