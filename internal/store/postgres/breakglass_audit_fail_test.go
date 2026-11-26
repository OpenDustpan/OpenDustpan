package postgres_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// postgresAuditFailTime is the time of the records of the tests below.
func postgresAuditFailTime() time.Time {
	return time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)
}

// TestBreakGlassSweepExemptionAppendFailsClosed proves that a failed append of
// the exemption record of a skipped break-glass account fails the sweep and
// rolls the transaction back: the account stays enabled and the server audit
// scope holds no new record. The store holds one inactive break-glass account,
// so the exemption record is the only entry that the run appends.
func TestBreakGlassSweepExemptionAppendFailsClosed(t *testing.T) {
	t.Parallel()

	f := newPostgresSweepFixture(t)
	bg := f.seed(t)
	before := f.serverEntryCount(t)

	fault := postgres.FailStatementForTest(f.store, "InsertAuditEntry")

	res, err := f.svc.SweepInactive(t.Context(), accesstoken.SweepEntry())
	if err == nil {
		t.Fatalf("sweep with a failed exemption record = %+v, want an error", res)
	}

	if fault.Fired() == 0 {
		t.Fatal("the fault of the exemption record did not fire")
	}

	fault.Restore()

	stored, getErr := f.principals.GetPrincipal(t.Context(), bg.ID)
	if getErr != nil || !stored.DisabledAt.IsZero() {
		t.Fatalf("break-glass account after the failed sweep = %+v, %v; want it enabled", stored, getErr)
	}

	if after := f.serverEntryCount(t); after != before {
		t.Fatalf("server audit entries after the failed sweep = %d, want %d", after, before)
	}
}

// TestBreakGlassAddPersonAuditAppendFailsClosed proves that a failed append of
// one of the two entries of an add of a break-glass person stores nothing: the
// person and its first passkey roll back with the entry.
func TestBreakGlassAddPersonAuditAppendFailsClosed(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL break-glass audit append test", envURL)
	}

	s := open(t, base)
	at := postgresAuditFailTime()

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

	fault := postgres.FailStatementForTest(s, "InsertAuditEntry")

	err := s.AddBreakGlassPerson(t.Context(), p, first,
		storetest.ServerChange(at, principal.Ref(p.ID)), storetest.ServerChange(at, "passkey:"+first.ID))
	if err == nil {
		t.Fatal("add with a failed audit append = nil, want an error")
	}

	if fault.Fired() == 0 {
		t.Fatal("the fault of the audit append did not fire")
	}

	fault.Restore()

	if _, getErr := s.GetPrincipal(t.Context(), p.ID); !errors.Is(getErr, principal.ErrNotFound) {
		t.Fatalf("person after the failed add = %v, want principal.ErrNotFound", getErr)
	}

	keys, keysErr := s.Passkeys(t.Context(), p.ID)
	if keysErr != nil || len(keys) != 0 {
		t.Fatalf("passkeys after the failed add = %+v, %v; want none", keys, keysErr)
	}
}

// TestBreakGlassRotateAuditAppendFailsClosed proves that a failed append of
// the entry of a rotation keeps the old passkey: the delete and the insert
// roll back with the entry.
func TestBreakGlassRotateAuditAppendFailsClosed(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL break-glass audit append test", envURL)
	}

	s := open(t, base)
	at := postgresAuditFailTime()

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

	fault := postgres.FailStatementForTest(s, "InsertAuditEntry")

	err := s.RotatePasskey(t.Context(), p.ID, old.ID, next, storetest.ServerChange(at, "passkey:"+next.ID))
	if err == nil {
		t.Fatal("rotation with a failed audit append = nil, want an error")
	}

	if fault.Fired() == 0 {
		t.Fatal("the fault of the audit append did not fire")
	}

	fault.Restore()

	keys, keysErr := s.Passkeys(t.Context(), p.ID)
	if keysErr != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys after the failed rotation = %+v, %v; want the old passkey %s only",
			keys, keysErr, old.ID)
	}
}
