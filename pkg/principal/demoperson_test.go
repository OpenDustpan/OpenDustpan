package principal_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// TestSeedDemoPerson proves that the demo path makes the person dev:demo, only
// that subject, as a local person with no namespace, no break-glass mark and no
// group, and that a later call returns the same person.
func TestSeedDemoPerson(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	first, created, err := f.svc.SeedDemoPerson(t.Context(), testEntry(), "Demo user")
	if err != nil || !created {
		t.Fatalf("SeedDemoPerson = created %v, %v; want a new person", created, err)
	}

	checkDemoPerson(t, first)

	second, created, err := f.svc.SeedDemoPerson(t.Context(), testEntry(), "Other name")
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second call = %s, created %v, %v; want the same person", second.ID, created, err)
	}

	page, err := f.svc.ListPrincipals(t.Context(),
		principal.PageRequest{Prefix: principal.DevSubjectPrefix, Cursor: "", Limit: 10}, "")
	if err != nil || page.Total != 1 {
		t.Fatalf("people with the dev prefix = %d, %v; want 1", page.Total, err)
	}
}

// TestDemoPersonValidateRefusesOtherSubjects proves the validation of the
// demo path: every other dev subject, and the dev administrator, fail with
// ErrReservedSubject. The path takes no subject, so the test sends the
// hostile subjects to the other layers as well.
func TestDemoPersonValidateRefusesOtherSubjects(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for _, subject := range append(devSubjects(), principal.DemoSubject, principal.DevAdminSubject) {
		if _, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(subject, true)); !errors.Is(
			err, principal.ErrReservedSubject,
		) {
			t.Errorf("SignInPrincipal(%q) = %v, want ErrReservedSubject", subject, err)
		}

		_, err := f.svc.CreatePrincipal(t.Context(), testEntry(), principal.Input{
			Subject: subject, DisplayName: "", Kind: principal.KindPerson,
		})
		if !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("CreatePrincipal(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

// checkDemoPerson proves that p is the local person dev:demo with no
// namespace, no break-glass mark, no group and no end.
func checkDemoPerson(t *testing.T, p principal.Principal) {
	t.Helper()

	if p.Subject != principal.DemoSubject || p.Kind != principal.KindPerson ||
		p.Source != principal.SourceLocal || p.Namespace != "" || p.BreakGlass ||
		p.GroupCount != 0 || !p.DisabledAt.IsZero() {
		t.Fatalf("the demo person = %+v, want a local person with the subject %s", p, principal.DemoSubject)
	}
}
