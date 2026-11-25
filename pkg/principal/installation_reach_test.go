package principal_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

type falseReach struct{}

func (falseReach) GrantsReachNamespaces(context.Context) (bool, error) {
	return false, nil
}

type trueReach struct{}

func (trueReach) GrantsReachNamespaces(context.Context) (bool, error) {
	return true, nil
}

var errReachRead = errors.New("reach read failed")

type errReach struct{}

func (errReach) GrantsReachNamespaces(context.Context) (bool, error) {
	return false, errReachRead
}

// openPrincipalService builds a principal service over a fresh store with the
// given reach port.
func openPrincipalService(
	t *testing.T, reach principal.InstallationReach,
) (principal.Service, *sqlite.Store) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	svc, err := principal.NewService(principal.Config{
		Store: st, Guard: principal.AdminOnlyGuard{}, GroupReach: nil, Namespaces: st,
		Tokens: nil, TempAdmins: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if reach != nil {
		svc = svc.WithInstallationReach(reach)
	}

	return *svc, st
}

func putNamespacePrincipal(t *testing.T, st *sqlite.Store, id string) {
	t.Helper()

	at := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}
	nsErr := st.CreateNamespace(t.Context(), ns)
	if nsErr != nil && !errors.Is(nsErr, namespace.ErrAlreadyExists) {
		t.Fatalf("create namespace: %v", nsErr)
	}

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: "prn-" + id,
		DisplayName: "d", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "acme", GroupCount: 0, BreakGlass: false,
	}
	fence, e := storetest.GrantWrite(t, st, identity.NamespaceObject("acme"), at, principal.Ref(id))
	if err := st.CreateNamespacePrincipal(t.Context(), p, fence, e); err != nil {
		t.Fatalf("create principal: %v", err)
	}
}

func adminCaller() identity.Principal {
	return identity.Principal{
		Subject: "", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "",
		JobID: "",
	}
}

// reachOp is one hostile case of the reach setting at the principal service.
type reachOp struct {
	name       string
	reach      principal.InstallationReach
	id         string
	wantDenied bool
	run        func(t *testing.T, svc principal.Service, id string) error
}

func updateOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		_, err := svc.UpdatePrincipal(t.Context(), testEntry(), pid, principal.Change{DisplayName: nil})

		return err
	}
}

func deleteOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.DeletePrincipal(t.Context(), testEntry(), adminCaller(), pid)
	}
}

func enableOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.EnablePrincipal(t.Context(), testEntry(), adminCaller(), pid)
	}
}

func disableOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.DisablePrincipal(t.Context(), testEntry(), adminCaller(), pid)
	}
}

func namespaceUpdateOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		_, err := svc.UpdateNamespacePrincipal(
			t.Context(), testEntry(), "acme", pid, principal.Change{DisplayName: nil},
		)

		return err
	}
}

func reachCases() []reachOp {
	return []reachOp{
		{name: "no port uses true", reach: nil, id: "p1", wantDenied: false, run: updateOp()},
		{name: "false denies update", reach: falseReach{}, id: "p2", wantDenied: true, run: updateOp()},
		{name: "false denies delete", reach: falseReach{}, id: "p3", wantDenied: true, run: deleteOp()},
		{name: "false denies enable", reach: falseReach{}, id: "p4", wantDenied: true, run: enableOp()},
		{name: "false denies disable", reach: falseReach{}, id: "p5", wantDenied: true, run: disableOp()},
		{name: "true allows update", reach: trueReach{}, id: "p6", wantDenied: false, run: updateOp()},
		{name: "unreadable denies", reach: errReach{}, id: "p7", wantDenied: true, run: updateOp()},
		{
			name: "namespace principal update still works", reach: falseReach{}, id: "p8",
			wantDenied: false, run: namespaceUpdateOp(),
		},
	}
}

// TestInstallationReachPrincipalService is the hostile case of the reach
// setting at the principal service layer.
func TestInstallationReachPrincipalService(t *testing.T) {
	t.Parallel()

	for _, tc := range reachCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, st := openPrincipalService(t, tc.reach)
			putNamespacePrincipal(t, st, tc.id)

			err := tc.run(t, svc, tc.id)
			if tc.wantDenied && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("%s = %v, want denied", tc.name, err)
			}

			if !tc.wantDenied && err != nil {
				t.Fatalf("%s = %v, want nil", tc.name, err)
			}
		})
	}
}
