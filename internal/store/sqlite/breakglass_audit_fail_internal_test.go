package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// failAuditInserts makes each later insert of an audit entry fail inside its
// write transaction: a trigger aborts the insert. A read still works.
func failAuditInserts(t *testing.T, s *Store) {
	t.Helper()

	err := s.ExecForTest(t.Context(), `CREATE TRIGGER break_glass_forced_error BEFORE INSERT ON audit_entries
BEGIN SELECT RAISE(ABORT, 'forced error'); END`)
	if err != nil {
		t.Fatalf("create the forced error trigger: %v", err)
	}
}

// openBreakGlassStore opens a fresh store with the baseline schema.
func openBreakGlassStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s
}

// breakGlassTestTime is the time of the records of the tests below.
func breakGlassTestTime() time.Time {
	return time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)
}

// serverEntries counts the entries of the server audit scope.
func serverEntries(t *testing.T, s *Store) int {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	return len(listing.Entries)
}

// TestSweepExemptionAppendFailsClosed proves that the sweep returns the error
// of a failed append of the exemption record and stores nothing: the
// transaction rolls back the disable of the inactive accounts. The store holds
// one inactive break-glass account, so the exemption record is the only entry
// that the run appends.
func TestSweepExemptionAppendFailsClosed(t *testing.T) {
	t.Parallel()

	s := openBreakGlassStore(t)
	// A fixed past time: the account is inactive for any clock of the store.
	at := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

	bg := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-bg-sweep",
		Subject: "bg@example.com", DisplayName: "bg@example.com", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}
	if err := s.CreatePrincipal(t.Context(), bg, storetest.ServerChange(at, principal.Ref(bg.ID))); err != nil {
		t.Fatalf("create the break-glass account: %v", err)
	}

	entries := serverEntries(t, s)
	failAuditInserts(t, s)

	sweepEntry := accesstoken.SweepEntry()
	res, err := s.SweepInactive(t.Context(),
		accesstoken.Sweep{Inactivity: time.Hour, Tokens: false, Accounts: true},
		func(at time.Time, object string) audit.Entry {
			e := sweepEntry
			e.At = at
			e.Namespace = audit.ServerScope
			e.Kind = audit.KindChange
			e.Outcome = audit.OutcomeSuccess
			e.Object = object

			return e
		})
	if err == nil {
		t.Fatalf("sweep with a failed exemption record = %+v, want an error", res)
	}

	stored, getErr := s.GetPrincipal(t.Context(), bg.ID)
	if getErr != nil || !stored.DisabledAt.IsZero() {
		t.Fatalf("break-glass account after the failed sweep = %+v, %v; want it enabled", stored, getErr)
	}

	if after := serverEntries(t, s); after != entries {
		t.Fatalf("server entries after the failed sweep = %d, want %d", after, entries)
	}
}

// TestSweepExemptionRefusesEntryOutsideServerScope proves that the sweep
// checks the exemption record that the entry function returns: an entry of a
// namespace scope is refused before the append, the transaction rolls back,
// and no scope gets an entry. The entry function is the only input, so the
// check of the store is the layer that the test reaches.
func TestSweepExemptionRefusesEntryOutsideServerScope(t *testing.T) {
	t.Parallel()

	s := openBreakGlassStore(t)
	// A fixed past time: the account is inactive for any clock of the store.
	at := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

	bg := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-bg-scope",
		Subject: "bg-scope@example.com", DisplayName: "bg-scope@example.com", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}
	if err := s.CreatePrincipal(t.Context(), bg, storetest.ServerChange(at, principal.Ref(bg.ID))); err != nil {
		t.Fatalf("create the break-glass account: %v", err)
	}

	entries := serverEntries(t, s)

	sweepEntry := accesstoken.SweepEntry()
	res, err := s.SweepInactive(t.Context(),
		accesstoken.Sweep{Inactivity: time.Hour, Tokens: false, Accounts: true},
		func(at time.Time, object string) audit.Entry {
			e := sweepEntry
			e.At = at
			e.Namespace = "team-a"
			e.Kind = audit.KindChange
			e.Outcome = audit.OutcomeSuccess
			e.Object = object

			return e
		})
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("sweep with an exemption record of a namespace = %+v, %v; want audit.ErrInvalidEntry", res, err)
	}

	stored, getErr := s.GetPrincipal(t.Context(), bg.ID)
	if getErr != nil || !stored.DisabledAt.IsZero() {
		t.Fatalf("break-glass account after the refused sweep = %+v, %v; want it enabled", stored, getErr)
	}

	if after := serverEntries(t, s); after != entries {
		t.Fatalf("server entries after the refused sweep = %d, want %d", after, entries)
	}

	listing, listErr := s.List(t.Context(), "team-a", audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if listErr != nil || len(listing.Entries) != 0 {
		t.Fatalf("team-a entries after the refused sweep = %+v, %v; want none", listing.Entries, listErr)
	}
}

// TestAddBreakGlassPersonAuditAppendFailsClosed proves that a failed append of
// one of the two entries of an add of a break-glass person stores nothing: the
// person and its first passkey roll back with the entry.
func TestAddBreakGlassPersonAuditAppendFailsClosed(t *testing.T) {
	t.Parallel()

	s := openBreakGlassStore(t)
	at := breakGlassTestTime()

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-bg-new",
		Subject: "bg-new@example.com", DisplayName: "", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}
	first := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-new", PersonID: p.ID, Name: "first-key",
		CredentialID: []byte("credential-new"), PublicKey: []byte("cose-key"),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}

	failAuditInserts(t, s)

	err := s.AddBreakGlassPerson(t.Context(), p, first,
		storetest.ServerChange(at, principal.Ref(p.ID)), storetest.ServerChange(at, "passkey:"+first.ID))
	if err == nil {
		t.Fatal("add with a failed audit append = nil, want an error")
	}

	if _, getErr := s.GetPrincipal(t.Context(), p.ID); !errors.Is(getErr, principal.ErrNotFound) {
		t.Fatalf("person after the failed add = %v, want principal.ErrNotFound", getErr)
	}

	keys, keysErr := s.Passkeys(t.Context(), p.ID)
	if keysErr != nil || len(keys) != 0 {
		t.Fatalf("passkeys after the failed add = %+v, %v; want none", keys, keysErr)
	}
}

// TestRotatePasskeyAuditAppendFailsClosed proves that a failed append of the
// entry of a rotation keeps the old passkey: the delete and the insert roll
// back with the entry.
func TestRotatePasskeyAuditAppendFailsClosed(t *testing.T) {
	t.Parallel()

	s := openBreakGlassStore(t)
	at := breakGlassTestTime()

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-bg-rotate",
		Subject: "bg-rotate@example.com", DisplayName: "", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}
	if err := s.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, principal.Ref(p.ID))); err != nil {
		t.Fatalf("create the break-glass account: %v", err)
	}

	old := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-old", PersonID: p.ID, Name: "old-key",
		CredentialID: []byte("credential-old"), PublicKey: []byte("cose-key"),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}
	if err := s.AddPasskey(t.Context(), old, storetest.ServerChange(at, "passkey:"+old.ID)); err != nil {
		t.Fatalf("add the old passkey: %v", err)
	}

	next := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-next", PersonID: p.ID, Name: "next-key",
		CredentialID: []byte("credential-next"), PublicKey: []byte("cose-key"),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}

	failAuditInserts(t, s)

	err := s.RotatePasskey(t.Context(), p.ID, old.ID, next, storetest.ServerChange(at, "passkey:"+next.ID))
	if err == nil {
		t.Fatal("rotation with a failed audit append = nil, want an error")
	}

	keys, keysErr := s.Passkeys(t.Context(), p.ID)
	if keysErr != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys after the failed rotation = %+v, %v; want the old passkey %s only",
			keys, keysErr, old.ID)
	}
}
