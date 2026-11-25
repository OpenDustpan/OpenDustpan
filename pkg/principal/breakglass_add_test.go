package principal_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestBreakGlassAddAndReport proves that the service adds a break-glass
// account at zero working accounts, that it refuses a kind that is not a
// person, and that the report reads the live count and the floor.
func TestBreakGlassAddAndReport(t *testing.T) {
	t.Parallel()

	t.Run("add at zero accounts", func(t *testing.T) {
		t.Parallel()
		checkBreakGlassAddAtZero(t)
	})

	t.Run("namespace scope counts only its own principals", func(t *testing.T) {
		t.Parallel()
		checkBreakGlassNamespaceScope(t)
	})

	t.Run("report names the missing accounts", func(t *testing.T) {
		t.Parallel()
		checkBreakGlassReport(t)
	})

	t.Run("disabled before the read is excluded", func(t *testing.T) {
		t.Parallel()
		checkBreakGlassReportDisabled(t)
	})
}

// checkBreakGlassAddAtZero adds the first account and proves its fields.
func checkBreakGlassAddAtZero(t *testing.T) {
	t.Helper()

	svc := newBreakGlassService(t)
	checkBreakGlassCount(t, svc, 0)

	account, err := svc.service.CreateBreakGlassAccount(t.Context(), testEntry(), principal.Input{
		Subject: "break-glass:recovery", DisplayName: "Recovery", Kind: principal.KindPerson,
	})
	if err != nil {
		t.Fatalf("add at zero accounts: %v", err)
	}

	if !account.BreakGlass || account.Kind != principal.KindPerson || account.Source != principal.SourceLocal ||
		account.Subject != "break-glass:recovery" || account.DisplayName != "Recovery" {
		t.Fatalf("account = %+v, want a local break-glass person", account)
	}

	stored, err := svc.store.GetPrincipal(t.Context(), account.ID)
	if err != nil || !stored.BreakGlass || stored.Subject != account.Subject {
		t.Fatalf("stored account = %+v, %v; want the break-glass person", stored, err)
	}

	// The account has no passkey yet, so it is not a working path.
	checkBreakGlassCount(t, svc, 0)
	checkBreakGlassRefusesService(t, svc)
}

// checkBreakGlassCount proves the live count of the service.
func checkBreakGlassCount(t *testing.T, svc breakGlassService, want int64) {
	t.Helper()

	count, err := svc.service.CountWorkingBreakGlass(t.Context(), svc.service.Now())
	if err != nil || count != want {
		t.Fatalf("working count = %d, %v; want %d", count, err, want)
	}
}

// checkBreakGlassRefusesService proves that the add refuses a service.
func checkBreakGlassRefusesService(t *testing.T, svc breakGlassService) {
	t.Helper()

	_, err := svc.service.CreateBreakGlassAccount(t.Context(), testEntry(), principal.Input{
		Subject: "break-glass:other", DisplayName: "", Kind: principal.KindService,
	})
	if !errors.Is(err, principal.ErrBreakGlassKind) {
		t.Fatalf("add of a service = %v, want ErrBreakGlassKind", err)
	}
}

// checkBreakGlassReport proves the count and the list of the accounts that
// exist without a working path.
func checkBreakGlassReport(t *testing.T) {
	t.Helper()

	svc := newBreakGlassService(t)
	storeBreakGlass(t, svc.store, "prn-bg1", "break-glass:one")
	storeBreakGlass(t, svc.store, "prn-bg2", "break-glass:two")

	count, err := svc.service.CountWorkingBreakGlass(t.Context(), svc.service.Now())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	// Neither account has a passkey or an administration grant.
	if count != 0 {
		t.Fatalf("working count = %d, want 0", count)
	}

	if principal.FloorWorkingPaths-count != 2 {
		t.Fatalf("missing count = %d, want 2", principal.FloorWorkingPaths-count)
	}

	accounts, err := svc.service.ListBreakGlassAccounts(t.Context())
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts = %+v, %v; want the two break-glass accounts", accounts, err)
	}
}

// storeWorkingBreakGlass stores an enabled break-glass account with a passkey
// and an administration grant, so the working count includes it.
func storeWorkingBreakGlass(t *testing.T, svc breakGlassService, id string) {
	t.Helper()

	ctx := t.Context()
	storeBreakGlass(t, svc.store, id, id+"@example.com")
	at := time.Now().Add(-time.Hour)

	key := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-" + strings.TrimPrefix(id, "prn-"), PersonID: id, Name: "recovery-key",
		CredentialID: []byte("credential-" + id), PublicKey: []byte("cose-key"),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}
	if err := key.Validate(); err != nil {
		t.Fatalf("validate passkey of %q: %v", id, err)
	}

	if err := svc.store.AddPasskey(ctx, key, storetest.ServerChange(at, "passkey:"+key.ID)); err != nil {
		t.Fatalf("add passkey of %q: %v", id, err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-" + id,
		Role: access.RoleSiteAdministrator, CreatedBy: "test",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}
	err := svc.store.CreateGrant(ctx, grant, fence, storetest.ServerChange(at, "grant:"+grant.ID), nil)
	if err != nil {
		t.Fatalf("create grant of %q: %v", id, err)
	}
}

// checkBreakGlassNamespaceScope proves that the count of a namespace scope holds
// only the working accounts of that namespace. The team namespace has one
// working account, which is below the floor, while the other namespace has two.
// The installation count does not change.
func checkBreakGlassNamespaceScope(t *testing.T) {
	t.Helper()

	svc := newBreakGlassService(t)
	ctx := t.Context()

	const team = namespace.Name("team-scope")

	const other = namespace.Name("other-scope")

	createTestNamespace(t, svc, team)
	createTestNamespace(t, svc, other)

	installBefore, err := svc.service.CountWorkingBreakGlass(ctx, svc.service.Now())
	if err != nil {
		t.Fatalf("installation count before the namespace accounts: %v", err)
	}

	storeNamespaceWorkingBreakGlass(t, svc, team, "prn-team-1")
	storeNamespaceWorkingBreakGlass(t, svc, other, "prn-other-1")
	storeNamespaceWorkingBreakGlass(t, svc, other, "prn-other-2")

	count, err := svc.store.CountWorkingBreakGlassInNamespace(ctx, team, svc.service.Now())
	if err != nil || count != 1 {
		t.Fatalf("working count of namespace %q = %d, %v; want 1", team, count, err)
	}

	if missing := principal.FloorWorkingPaths - count; missing != 1 {
		t.Fatalf("missing count of namespace %q = %d, want 1", team, missing)
	}

	otherCount, err := svc.store.CountWorkingBreakGlassInNamespace(ctx, other, svc.service.Now())
	if err != nil || otherCount != 2 {
		t.Fatalf("working count of namespace %q = %d, %v; want 2", other, otherCount, err)
	}

	installAfter, err := svc.service.CountWorkingBreakGlass(ctx, svc.service.Now())
	if err != nil || installAfter != installBefore {
		t.Fatalf("installation count = %d, %v; want %d, the count before the namespace accounts",
			installAfter, err, installBefore)
	}
}

// createTestNamespace stores a namespace named name for the break-glass tests.
func createTestNamespace(t *testing.T, svc breakGlassService, name namespace.Name) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Unix(0, 0),
		Name:                name,
		PrimaryInstallation: "",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := svc.store.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}
}

// storeNamespaceWorkingBreakGlass stores an enabled local break-glass person of
// the namespace name, with a passkey and a site-administrator grant on that
// namespace, so that the count of the namespace includes it.
func storeNamespaceWorkingBreakGlass(t *testing.T, svc breakGlassService, name namespace.Name, id string) {
	t.Helper()

	ctx := t.Context()
	at := time.Now().Add(-time.Hour)
	fence := namespace.Fence{Namespace: name, FailoverVersion: 1}

	p := principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), DisabledAt: time.Time{},
		ID: id, Subject: id + "@example.com", DisplayName: id, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: name, GroupCount: 0, BreakGlass: true,
	}

	personEntry := storetest.ServerChange(at, "principal:"+id)
	personEntry.Namespace = name

	if err := svc.store.CreateNamespacePrincipal(ctx, p, fence, personEntry); err != nil {
		t.Fatalf("create namespace principal %q: %v", id, err)
	}

	key := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-" + id, PersonID: id, Name: "recovery-key",
		CredentialID: []byte("credential-" + id), PublicKey: []byte("cose-key"),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}
	if err := key.Validate(); err != nil {
		t.Fatalf("validate passkey of %q: %v", id, err)
	}

	if err := svc.store.AddPasskey(ctx, key, storetest.ServerChange(at, "passkey:"+key.ID)); err != nil {
		t.Fatalf("add passkey of %q: %v", id, err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.NamespaceObject(name), ID: "grt-" + id,
		Role: access.RoleSiteAdministrator, CreatedBy: "test",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	grantEntry := storetest.ServerChange(at, "grant:"+grant.ID)
	grantEntry.Namespace = name

	if err := svc.store.CreateGrant(ctx, grant, fence, grantEntry, nil); err != nil {
		t.Fatalf("create grant of %q: %v", id, err)
	}
}

// checkBreakGlassReportDisabled proves the report-count scenario of an account
// that is disabled before the read: the working count excludes it, the missing
// count rises, and the list shows it as disabled.
func checkBreakGlassReportDisabled(t *testing.T) {
	t.Helper()

	svc := newBreakGlassService(t)
	ctx := t.Context()

	storeWorkingBreakGlass(t, svc, "prn-bg1")
	storeWorkingBreakGlass(t, svc, "prn-bg2")

	before, err := svc.service.CountWorkingBreakGlass(ctx, svc.service.Now())
	if err != nil || before != 2 {
		t.Fatalf("working count before the disable = %d, %v; want 2", before, err)
	}

	entry := storetest.ServerChange(time.Now(), principal.Ref("prn-bg1"))
	if disableErr := svc.store.DisablePrincipal(ctx, "prn-bg1", entry); disableErr != nil {
		t.Fatalf("disable prn-bg1: %v", disableErr)
	}

	after, err := svc.service.CountWorkingBreakGlass(ctx, svc.service.Now())
	if err != nil || after != 1 {
		t.Fatalf("working count after the disable = %d, %v; want 1", after, err)
	}

	if missing := int64(principal.FloorWorkingPaths) - after; missing != 1 {
		t.Fatalf("missing count = %d, want 1", missing)
	}

	accounts, err := svc.service.ListBreakGlassAccounts(ctx)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts = %+v, %v; want the two break-glass accounts", accounts, err)
	}

	for i := range accounts {
		disabled := !accounts[i].DisabledAt.IsZero()
		if (accounts[i].ID == "prn-bg1") != disabled {
			t.Fatalf("account %s disabled = %v; want %v", accounts[i].ID, disabled,
				accounts[i].ID == "prn-bg1")
		}
	}
}
