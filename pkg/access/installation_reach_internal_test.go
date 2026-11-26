package access

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// serverGrant returns a grant on the installation for the confine tests.
func serverGrant() Grant {
	return Grant{
		CreatedAt: time.Unix(0, 0),
		Condition: nil,
		Object:    identity.ServerObject(),
		ID:        "grt-confine",
		Role:      RoleViewer,
		CreatedBy: "admin",
		Subject:   Subject{Kind: SubjectPrincipal, ID: "prn-holder"},
	}
}

// TestConfineInstallationGrantsDropsServerGrant proves the defense-in-depth
// guard: when the chain of a namespace object does not hold the
// installation, a grant on the installation is dropped, even if a store
// returned it. The case fails when confineInstallationGrants returns its
// input unchanged.
func TestConfineInstallationGrantsDropsServerGrant(t *testing.T) {
	t.Parallel()

	grants := []Grant{serverGrant()}

	chain := []identity.Object{identity.NamespaceObject("acme")}

	got := confineInstallationGrants(grants, chain)

	if len(got) != 0 {
		t.Fatalf("confineInstallationGrants = %d grants, want 0 (the @server grant must be dropped)", len(got))
	}
}

// TestConfineInstallationGrantsKeepsServerGrantWhenChainHoldsIt proves the
// guard keeps a grant on the installation when the chain holds the
// installation.
func TestConfineInstallationGrantsKeepsServerGrantWhenChainHoldsIt(t *testing.T) {
	t.Parallel()

	grants := []Grant{serverGrant()}

	chain := []identity.Object{identity.ServerObject(), identity.NamespaceObject("acme")}

	got := confineInstallationGrants(grants, chain)

	if len(got) != 1 {
		t.Fatalf("confineInstallationGrants = %d grants, want 1", len(got))
	}
}
