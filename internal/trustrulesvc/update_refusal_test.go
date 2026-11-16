package trustrulesvc_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TestUpdateRefusesAnInvalidRule sends an update that leaves the rule with
// no conditions, as if every other check had failed: the Validate guard
// refuses it before the issuer, the action, or the principal checks run,
// and nothing reaches the store.
func TestUpdateRefusesAnInvalidRule(t *testing.T) {
	t.Parallel()

	stored := seededRule()
	f := updateRefusalFixture(t, stored, nil)

	in := renameInput(stored.ID, "", 1)
	in.ConditionsSet = true
	in.Conditions = nil

	_, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("Update = %v, want ErrInvalid", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused update reached the store")
	}
}

// TestUpdateRefusesAChangedIssuer sends an update that moves the rule to an
// issuer of this installation: the issuer guard refuses it, and nothing
// reaches the store.
func TestUpdateRefusesAChangedIssuer(t *testing.T) {
	t.Parallel()

	stored := seededRule()
	f := updateRefusalFixture(t, stored, nil)

	in := renameInput(stored.ID, "", 1)
	in.Issuer = "https://dustpan.example.com/oidc/ns-1"

	_, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, trustrule.ErrIssuerRefused) {
		t.Fatalf("Update = %v, want ErrIssuerRefused", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused update reached the store")
	}
}

// TestUpdateNeedsManageTokensOnThePrincipal sends an update that points the
// rule at another principal when the caller holds no manage-tokens action:
// the authorization at the point of use refuses it before the principal is
// read, so the caller never learns whether the ID names a principal.
func TestUpdateNeedsManageTokensOnThePrincipal(t *testing.T) {
	t.Parallel()

	stored := seededRule()
	f := updateRefusalFixture(t, stored, nil)
	f.authz.allow = nil

	in := renameInput(stored.ID, "", 1)
	in.PrincipalID = "prn-stranger"

	_, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, trustrulesvc.ErrManageTokensDenied) {
		t.Fatalf("Update = %v, want ErrDenied and ErrManageTokensDenied", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the denied update reached the store")
	}
}

// TestUpdateRefusesAPrincipalOutsideTheNamespace sends an update that points
// the rule at each principal that cannot hold a rule of the namespace, as if
// every other check had failed. Defense in depth: the store checks it again.
func TestUpdateRefusesAPrincipalOutsideTheNamespace(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"prn-unknown", "prn-disabled", "prn-person", "prn-server", "prn-stranger"} {
		stored := seededRule()
		f := updateRefusalFixture(t, stored, nil)

		in := renameInput(stored.ID, "", 1)
		in.PrincipalID = id

		_, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
		if !errors.Is(err, trustrule.ErrPrincipal) {
			t.Errorf("principal %s: Update = %v, want ErrPrincipal", id, err)
		}

		if len(f.store.created) != 0 {
			t.Errorf("principal %s: the refused update reached the store", id)
		}
	}
}
