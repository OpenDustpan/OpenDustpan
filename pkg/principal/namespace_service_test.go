package principal_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// serviceSubjects returns the hostile subjects of the namespace service
// principal that every layer below refuses. Each one starts with the
// reserved prefix, in any case, and each one has the form of a valid subject.
func serviceSubjects() []string {
	return []string{
		"system:namespace-service:acme",
		"System:Namespace-Service:acme",
		"SYSTEM:NAMESPACE-SERVICE:ACME",
		"system:namespace-service:acme/extra",
		"system:namespace-service:",
		"system:namespace-service:acme-extra-text",
	}
}

// Each of the following tests sends the hostile subject straight to one
// layer, as if the other layers had failed.

func TestValidateSubjectRefusesNamespaceServicePrefix(t *testing.T) {
	t.Parallel()

	for _, subject := range serviceSubjects() {
		if err := principal.ValidateSubject(subject); !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("ValidateSubject(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}

	if err := principal.ValidateSubject("system:namespace-servic:acme"); err != nil {
		t.Errorf("ValidateSubject of a subject that only looks similar = %v, want nil", err)
	}
}

func TestPrincipalValidateRefusesNamespaceServicePrefix(t *testing.T) {
	t.Parallel()

	for _, subject := range serviceSubjects() {
		if err := stored("prn-x", subject).Validate(); !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("Validate(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

func TestCreatePrincipalRefusesNamespaceServicePrefix(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for _, subject := range serviceSubjects() {
		for _, kind := range []principal.Kind{principal.KindPerson, principal.KindService} {
			_, err := f.svc.CreatePrincipal(t.Context(), testEntry(), principal.Input{
				Subject: subject, DisplayName: "", Kind: kind,
			})
			if !errors.Is(err, principal.ErrReservedSubject) {
				t.Errorf("CreatePrincipal(%q, %s) = %v, want ErrReservedSubject", subject, kind, err)
			}
		}
	}
}

func TestCreateNamespacePrincipalRefusesNamespaceServicePrefix(t *testing.T) {
	t.Parallel()

	st := openStore(t)

	err := st.CreateNamespace(t.Context(), namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: st,
		Tokens:     nil,
		GroupReach: nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	for _, subject := range serviceSubjects() {
		_, err := svc.CreateNamespacePrincipal(t.Context(), testEntry(), "acme", principal.NamespaceInput{
			Subject: subject, DisplayName: "",
		})
		if !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("CreateNamespacePrincipal(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

func TestSignInPrincipalRefusesNamespaceServicePrefix(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for _, create := range []bool{true, false} {
		for _, subject := range serviceSubjects() {
			_, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(subject, create))
			if !errors.Is(err, principal.ErrReservedSubject) {
				t.Errorf("SignInPrincipal(%q, create %t) = %v, want ErrReservedSubject", subject, create, err)
			}
		}
	}
}

// TestSignInPrincipalRefusesAPlantedNamespaceServiceRecord proves that the
// refusal comes before the read of the store: a record that a restore put in
// the store never signs in.
func TestSignInPrincipalRefusesAPlantedNamespaceServiceRecord(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)
	f.put(t, stored("prn-svc", "system:namespace-service:acme"))

	p, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("system:namespace-service:acme", false))
	if !errors.Is(err, principal.ErrReservedSubject) || p.ID != "" {
		t.Fatalf("SignInPrincipal of the planted record = %q, %v; want no principal and ErrReservedSubject", p.ID, err)
	}
}

// TestPlantedNamespaceServiceRecordIsNotEditable proves that no API changes,
// disables, enables, deletes or groups a record with the subject of the
// namespace service principal, also for an administrator.
func TestPlantedNamespaceServiceRecordIsNotEditable(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)
	f.put(t, stored("prn-svc", "system:namespace-service:acme"))

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "",
		Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	group, err := f.svc.CreateGroup(t.Context(), testEntry(), principal.GroupInput{Name: "ops", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	name := "other"
	steps := map[string]func() error{
		"update": func() error {
			_, e := f.svc.UpdatePrincipal(t.Context(), testEntry(), "prn-svc", principal.Change{DisplayName: &name})

			return e
		},
		"delete":  func() error { return f.svc.DeletePrincipal(t.Context(), testEntry(), admin, "prn-svc") },
		"disable": func() error { return f.svc.DisablePrincipal(t.Context(), testEntry(), admin, "prn-svc") },
		"enable":  func() error { return f.svc.EnablePrincipal(t.Context(), testEntry(), admin, "prn-svc") },
		"add member": func() error {
			_, e := f.svc.AddMember(t.Context(), testEntry(), admin, group.ID, "prn-svc")

			return e
		},
	}

	for step, run := range steps {
		if runErr := run(); !errors.Is(runErr, principal.ErrReservedSubject) {
			t.Errorf("%s of the planted record = %v, want ErrReservedSubject", step, runErr)
		}
	}

	got, err := f.svc.GetPrincipal(t.Context(), "prn-svc")
	if err != nil || !got.DisabledAt.IsZero() || got.GroupCount != 0 ||
		got.DisplayName != "system:namespace-service:acme" {
		t.Fatalf("record after the refused steps = %+v, %v; want it unchanged", got, err)
	}
}
