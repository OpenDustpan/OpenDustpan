package access_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestCanActAs sends each kind of maker to the decision of the act-as path:
// a path through a direct grant, through a group, on the installation and
// as a bootstrap admin holds; no grant, a grant of another principal, an
// ended membership, a disabled maker, a maker that is not a principal, an
// empty maker and a principal ID that does not parse hold none.
func TestCanActAs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.installationService(t, "prn-ci", "ci-deploy")
	f.installationService(t, "prn-other", "other-bot")

	for _, subject := range []string{"tina", "gina", "olga", "nora", "dora", "lena"} {
		f.person(t, subject)
	}

	ci, err := identity.ParsePrincipalObject("prn-ci")
	if err != nil {
		t.Fatalf("parse the principal object: %v", err)
	}

	other, err := identity.ParsePrincipalObject("prn-other")
	if err != nil {
		t.Fatalf("parse the principal object: %v", err)
	}

	f.grant(t, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-tina"}, access.RoleTokenManager, ci)
	f.grant(t, f.group(t, "deployers", "gina"), access.RoleTokenManager, ci)
	f.grant(t, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-olga"}, access.RoleTokenManager,
		identity.ServerObject())
	f.grant(t, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-nora"}, access.RoleTokenManager, other)
	f.grant(t, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-dora"}, access.RoleTokenManager, ci)
	f.grant(t, f.group(t, "former", "lena"), access.RoleTokenManager, ci)

	if err := f.store.DisablePrincipal(t.Context(), "prn-dora",
		storetest.ServerChange(time.Unix(1, 0), "principal:prn-dora")); err != nil {
		t.Fatalf("disable dora: %v", err)
	}

	if err := f.store.RemoveMember(t.Context(), "grp-former", "prn-lena",
		storetest.ServerChange(time.Unix(1, 0), "group:grp-former")); err != nil {
		t.Fatalf("remove lena: %v", err)
	}

	tests := []struct {
		name        string
		maker       string
		principalID string
		want        bool
	}{
		{"a direct grant on the principal", "tina", "prn-ci", true},
		{"a grant to a group of the maker", "gina", "prn-ci", true},
		{"a grant on the installation", "olga", "prn-ci", true},
		{"a bootstrap admin", "admin", "prn-ci", true},
		{"a grant on another principal", "nora", "prn-ci", false},
		{"a disabled maker", "dora", "prn-ci", false},
		{"an ended membership", "lena", "prn-ci", false},
		{"a maker that is not a principal", "ghost", "prn-ci", false},
		{"an empty maker", "", "prn-ci", false},
		{"a principal ID that does not parse", "tina", "../prn-ci", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := f.svc.CanActAs(t.Context(), tc.maker, tc.principalID)
			if err != nil {
				t.Fatalf("CanActAs(%q, %q): %v", tc.maker, tc.principalID, err)
			}

			if got != tc.want {
				t.Fatalf("CanActAs(%q, %q) = %t, want %t", tc.maker, tc.principalID, got, tc.want)
			}
		})
	}
}
