package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// breakGlassList is the answer of the list route.
type breakGlassList struct {
	Accounts []breakGlassAccountDoc `json:"accounts"`
	Working  int64                  `json:"working"`
	Missing  int64                  `json:"missing"`
	Floor    int                    `json:"floor"`
}

// listBreakGlassAs sends the list request in a fresh console session of
// subject and decodes the answer.
func listBreakGlassAs(t *testing.T, f accessFixture, subject string) breakGlassList {
	t.Helper()

	rec := f.freshPasskeyClient(t, subject).do(http.MethodGet, breakGlassPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", rec.Code, rec.Body.String())
	}

	var list breakGlassList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the list %q: %v", rec.Body.String(), err)
	}

	return list
}

// TestBreakGlassAddedAccountWithoutGrantDoesNotRaiseCount proves that an added
// break-glass account is listed, and that it does not count as working until
// an administrator grants it an administration role. The add route creates no
// grant: a holder of server:manage-principals cannot make an administrator
// through the add. The list reports the floor shortfall as before the add.
func TestBreakGlassAddedAccountWithoutGrantDoesNotRaiseCount(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")

	rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s, want 201", rec.Code, rec.Body.String())
	}

	var account breakGlassAccountDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode the account %q: %v", rec.Body.String(), err)
	}

	list := listBreakGlassAs(t, f, "admin")

	listed := false
	for _, a := range list.Accounts {
		if a.ID == account.ID {
			listed = true
		}
	}

	if !listed {
		t.Fatalf("list accounts = %+v, want the added account %q", list.Accounts, account.ID)
	}

	if list.Working != 0 || list.Missing != 2 || list.Floor != 2 {
		t.Fatalf("counts after an add without a grant = %+v, want 0 working, 2 missing and the floor 2", list)
	}
}

// TestBreakGlassListCountsTheInstallationScope proves that the counts of the
// list route are the counts of the installation. Two working break-glass
// accounts of the namespace acme meet the floor of acme, but the list still
// reports the installation below the floor: the namespace count is used by
// the guard and the restore check, not by this route.
func TestBreakGlassListCountsTheInstallationScope(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")

	for _, subject := range []string{"bg-acme-1", "bg-acme-2"} {
		seedNamespaceBreakGlass(t, f, "acme", subject)
	}

	inAcme, err := f.store.CountWorkingBreakGlassInNamespace(t.Context(), "acme", f.sessions.Now())
	if err != nil || inAcme != 2 {
		t.Fatalf("working count of acme = %d, %v; want 2", inAcme, err)
	}

	list := listBreakGlassAs(t, f, "admin")
	if list.Working != 0 || list.Missing != 2 || list.Floor != 2 {
		t.Fatalf("list counts with two working accounts in acme = %+v, want 0 working, 2 missing and the floor 2", list)
	}
}

// TestBreakGlassListHoldsOnlyInstallationAccounts proves that the list route
// on @server lists the break-glass accounts of the installation only, the same
// set that the rotate route reads and that the working count counts. A working
// account of a namespace is not listed and does not raise the count.
func TestBreakGlassListHoldsOnlyInstallationAccounts(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")
	seedNamespaceBreakGlass(t, f, "acme", "bg-acme-1")

	inAcme, err := f.store.CountWorkingBreakGlassInNamespace(t.Context(), "acme", f.sessions.Now())
	if err != nil || inAcme != 1 {
		t.Fatalf("working count of acme = %d, %v; want 1", inAcme, err)
	}

	list := listBreakGlassAs(t, f, "admin")

	for _, a := range list.Accounts {
		if a.ID == "prn-bg-acme-1" {
			t.Fatalf("list accounts = %+v, want no account of a namespace", list.Accounts)
		}
	}

	if list.Working != 0 || list.Missing != 2 || list.Floor != 2 {
		t.Fatalf("list counts with one working account in acme = %+v, want 0 working, 2 missing and the floor 2",
			list)
	}
}

// seedNamespaceBreakGlass stores a working break-glass account of ns: the
// person "prn-<subject>" of ns, one passkey and a grant of admin on ns.
func seedNamespaceBreakGlass(t *testing.T, f accessFixture, ns namespace.Name, subject string) {
	t.Helper()

	at := f.sessions.Now().Add(-time.Hour)
	id := "prn-" + subject

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: ns, GroupCount: 0, BreakGlass: true,
	}

	fence, e := storetest.GrantWrite(t, f.store, identity.NamespaceObject(ns), at, principal.Ref(id))
	if err := f.store.CreateNamespacePrincipal(t.Context(), p, fence, e); err != nil {
		t.Fatalf("seed namespace break-glass person %q: %v", id, err)
	}

	key := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-" + subject, PersonID: id, Name: "key-" + subject,
		CredentialID: []byte("credential-" + subject), PublicKey: []byte("cose-key-" + subject),
		Transports: []string{mfa.TransportUSB}, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}

	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "caller:manage-sign-in", Object: "passkey:" + key.ID, Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}

	if err := f.store.AddPasskey(t.Context(), key, entry); err != nil {
		t.Fatalf("seed the passkey of %q: %v", id, err)
	}

	f.grant(t, subject, access.RoleAdmin, identity.NamespaceObject(ns))
}
