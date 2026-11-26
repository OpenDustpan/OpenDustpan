package principal_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
		Store: st, Guard: permissiveGuard{}, GroupReach: nil, Namespaces: st,
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

// permissiveGuard allows every change, so a test reaches the reach check of
// the service and not only the guard.
type permissiveGuard struct{}

func (permissiveGuard) CheckGroupGrants(
	context.Context, identity.Principal, string, string, principal.GroupChange,
) error {
	return nil
}

func (permissiveGuard) CheckPrincipalGrants(context.Context, identity.Principal, string) error {
	return nil
}

// nonAdminCaller returns a caller that the reach check limits.
func nonAdminCaller() identity.Principal {
	return identity.Principal{
		Subject: "", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "",
		JobID: "",
	}
}

// adminCaller returns the bootstrap administrator caller, which the reach
// check does not limit.
func adminCaller() identity.Principal {
	caller := nonAdminCaller()
	caller.Admin = true

	return caller
}

// reachOp is one hostile case of the reach setting at the principal service.
type reachOp struct {
	name       string
	reach      principal.InstallationReach
	id         string
	wantDenied bool
	// disabled prepares the principal in the disabled state, which the
	// enable cases need.
	disabled bool
	run      func(t *testing.T, svc principal.Service, id string) error
}

func updateOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		_, err := svc.UpdatePrincipal(
			t.Context(), testEntry(), nonAdminCaller(), pid, principal.Change{DisplayName: nil},
		)

		return err
	}
}

func deleteOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.DeletePrincipal(t.Context(), testEntry(), nonAdminCaller(), pid)
	}
}

func enableOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.EnablePrincipal(t.Context(), testEntry(), nonAdminCaller(), pid)
	}
}

func disableOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.DisablePrincipal(t.Context(), testEntry(), nonAdminCaller(), pid)
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

func namespaceDeleteOp() func(t *testing.T, svc principal.Service, pid string) error {
	return func(t *testing.T, svc principal.Service, pid string) error {
		t.Helper()

		return svc.DeleteNamespacePrincipal(t.Context(), testEntry(), nonAdminCaller(), "acme", pid)
	}
}

func reachCases() []reachOp {
	return []reachOp{
		{name: "no port uses true", reach: nil, id: "p1", wantDenied: false, disabled: false, run: updateOp()},
		{
			name: "false denies update", reach: falseReach{}, id: "p2", wantDenied: true, disabled: false,
			run: updateOp(),
		},
		{
			name: "false denies delete", reach: falseReach{}, id: "p3", wantDenied: true, disabled: false,
			run: deleteOp(),
		},
		{
			name: "false denies enable", reach: falseReach{}, id: "p4", wantDenied: true, disabled: true,
			run: enableOp(),
		},
		{
			name: "false denies disable", reach: falseReach{}, id: "p5", wantDenied: true, disabled: false,
			run: disableOp(),
		},
		{name: "true allows update", reach: trueReach{}, id: "p6", wantDenied: false, disabled: false, run: updateOp()},
		{
			name: "true allows delete", reach: trueReach{}, id: "p10", wantDenied: false, disabled: false,
			run: deleteOp(),
		},
		{
			name: "true allows enable", reach: trueReach{}, id: "p11", wantDenied: false, disabled: true,
			run: enableOp(),
		},
		{
			name: "true allows disable", reach: trueReach{}, id: "p12", wantDenied: false, disabled: false,
			run: disableOp(),
		},
		{
			name: "unreadable denies", reach: errReach{}, id: "p7", wantDenied: true, disabled: false, run: updateOp(),
		},
		{
			name: "namespace principal update still works", reach: falseReach{}, id: "p8",
			wantDenied: false, disabled: false, run: namespaceUpdateOp(),
		},
		{
			name: "namespace principal delete still works", reach: falseReach{}, id: "p13",
			wantDenied: false, disabled: false, run: namespaceDeleteOp(),
		},
	}
}

// TestInstallationReachPrincipalServiceAdmin checks that the setting false
// does not limit the bootstrap administrator token.
func TestInstallationReachPrincipalServiceAdmin(t *testing.T) {
	t.Parallel()

	svc, st := openPrincipalService(t, falseReach{})
	putNamespacePrincipal(t, st, "p9")

	caller := adminCaller()

	_, err := svc.UpdatePrincipal(t.Context(), testEntry(), caller, "p9", principal.Change{DisplayName: nil})
	if err != nil {
		t.Fatalf("update as admin = %v, want nil", err)
	}

	err = svc.DisablePrincipal(t.Context(), testEntry(), caller, "p9")
	if err != nil {
		t.Fatalf("disable as admin = %v, want nil", err)
	}
}

// TestInstallationReachPrincipalService is the hostile case of the reach
// setting at the principal service layer.
func TestInstallationReachPrincipalService(t *testing.T) {
	t.Parallel()

	for _, tc := range reachCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runReachCase(t, tc)
		})
	}
}

// runReachCase prepares one principal and runs the operation of the case.
func runReachCase(t *testing.T, tc reachOp) {
	t.Helper()

	svc, st := openPrincipalService(t, tc.reach)
	putNamespacePrincipal(t, st, tc.id)

	if tc.disabled {
		if derr := svc.DisablePrincipal(t.Context(), testEntry(), adminCaller(), tc.id); derr != nil {
			t.Fatalf("prepare a disabled principal: %v", derr)
		}
	}

	err := tc.run(t, svc, tc.id)

	if tc.wantDenied {
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("%s = %v, want denied", tc.name, err)
		}

		return
	}

	if err != nil {
		t.Fatalf("%s = %v, want nil", tc.name, err)
	}
}

// putInstallationPrincipal stores a service principal of the installation.
func putInstallationPrincipal(t *testing.T, st *sqlite.Store, id string) {
	t.Helper()

	at := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: id,
		DisplayName: "d", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, principal.Ref(id))); err != nil {
		t.Fatalf("create principal: %v", err)
	}
}

// principalListCase is one case of the list of principals.
type principalListCase struct {
	name    string
	reach   principal.InstallationReach
	want    []string
	wantErr bool
}

// TestInstallationReachPrincipalList checks the list of principals: with the
// setting false it lists the principals of the installation only, with true it
// lists every principal, and a failed read returns the error.
func TestInstallationReachPrincipalList(t *testing.T) {
	t.Parallel()

	tests := []principalListCase{
		{
			name: "false lists the installation only", reach: falseReach{}, want: []string{"prn-inst"},
			wantErr: false,
		},
		{
			name: "true lists every principal", reach: trueReach{}, want: []string{"prn-inst", "prn-ns"},
			wantErr: false,
		},
		{name: "an unreadable setting returns the error", reach: errReach{}, want: nil, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runPrincipalListCase(t, tc)
		})
	}
}

// runPrincipalListCase prepares the principals and checks the page of one
// case.
func runPrincipalListCase(t *testing.T, tc principalListCase) {
	t.Helper()

	svc, st := openPrincipalService(t, tc.reach)

	putInstallationPrincipal(t, st, "prn-inst")
	putNamespacePrincipal(t, st, "prn-ns")

	page, err := svc.ListPrincipals(t.Context(), principal.PageRequest{Prefix: "", Cursor: "", Limit: 0}, "")
	if tc.wantErr {
		if !errors.Is(err, errReachRead) {
			t.Fatalf("ListPrincipals error = %v, want it to wrap errReachRead", err)
		}

		return
	}

	if err != nil {
		t.Fatalf("ListPrincipals = %v, want nil", err)
	}

	ids := make([]string, 0, len(page.Items))
	for i := range page.Items {
		ids = append(ids, page.Items[i].ID)
	}

	slices.Sort(ids)

	if !slices.Equal(ids, tc.want) {
		t.Fatalf("ListPrincipals = %v, want %v", ids, tc.want)
	}

	if page.Total != int64(len(tc.want)) {
		t.Fatalf("ListPrincipals total = %d, want %d", page.Total, len(tc.want))
	}
}
