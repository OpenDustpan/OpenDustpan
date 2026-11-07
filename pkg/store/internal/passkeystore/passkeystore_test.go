package passkeystore_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/passkeystore"
)

// row returns a row that every check passes.
func row() passkeystore.Row {
	return passkeystore.Row{
		ID: "pky-1", PrincipalID: "prn-1", Name: "laptop", Transports: "usb,internal",
		CredentialID: []byte("credential"), PublicKey: []byte("key"), AAGUID: make([]byte, 16),
		SignCount: 7, CreatedAtMs: 1_000, LastUsedAtMs: 0, SuspendedAtMs: 0, BackupEligible: true,
	}
}

// TestPasskeyOfReadsAGoodRow checks the read of a row that a write made.
func TestPasskeyOfReadsAGoodRow(t *testing.T) {
	t.Parallel()

	p, err := passkeystore.PasskeyOf(row())
	if err != nil {
		t.Fatalf("passkey of a good row: %v", err)
	}

	if p.SignCount != 7 || len(p.Transports) != 2 || !p.LastUsedAt.IsZero() || !p.SuspendedAt.IsZero() {
		t.Fatalf("passkey = %+v, want counter 7, two transports and no use", p)
	}
}

// TestPasskeyOfRefusesAChangedRow sends rows that no write makes, as a
// restored backup or another version could hold them. Each fails with
// mfa.ErrInvalid, so no such row reaches a ceremony.
func TestPasskeyOfRefusesAChangedRow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		change func(r *passkeystore.Row)
		name   string
	}{
		{name: "negative counter", change: func(r *passkeystore.Row) { r.SignCount = -1 }},
		{name: "counter above 32 bits", change: func(r *passkeystore.Row) { r.SignCount = math.MaxUint32 + 1 }},
		{name: "no time of the add", change: func(r *passkeystore.Row) { r.CreatedAtMs = 0 }},
		{name: "negative suspension", change: func(r *passkeystore.Row) { r.SuspendedAtMs = -5 }},
		{name: "short AAGUID", change: func(r *passkeystore.Row) { r.AAGUID = []byte{1} }},
		{name: "unknown transport", change: func(r *passkeystore.Row) { r.Transports = "usb,telepathy" }},
		{name: "empty transport", change: func(r *passkeystore.Row) { r.Transports = "usb,,nfc" }},
		{name: "ID with no prefix", change: func(r *passkeystore.Row) { r.ID = "key-1" }},
		{name: "name with a line break", change: func(r *passkeystore.Row) { r.Name = "a\nb" }},
		{name: "long key", change: func(r *passkeystore.Row) {
			r.PublicKey = []byte(strings.Repeat("k", mfa.MaxPublicKeyBytes+1))
		}},
		{name: "no credential ID", change: func(r *passkeystore.Row) { r.CredentialID = nil }},
	}

	for _, c := range cases {
		r := row()
		c.change(&r)

		if _, err := passkeystore.PasskeyOf(r); !errors.Is(err, mfa.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", c.name, err)
		}
	}
}

// TestChallengeOfRefusesAChangedRow sends challenge rows that no write
// makes. Each fails with mfa.ErrInvalid, so no such row starts a ceremony.
func TestChallengeOfRefusesAChangedRow(t *testing.T) {
	t.Parallel()

	good := passkeystore.ChallengeRow{
		Digest: strings.Repeat("a", 64), Kind: "fresh", PrincipalID: "prn-1", PendingID: "", SessionID: "ses-1",
		Source: "", SourceGroup: "", CreatedAtMs: 1_000, ExpiresAtMs: 2_000,
	}

	if _, err := passkeystore.ChallengeOf(good); err != nil {
		t.Fatalf("challenge of a good row: %v", err)
	}

	cases := []struct {
		change func(r *passkeystore.ChallengeRow)
		name   string
	}{
		{name: "unknown kind", change: func(r *passkeystore.ChallengeRow) { r.Kind = "admin" }},
		{name: "long lifetime", change: func(r *passkeystore.ChallengeRow) { r.ExpiresAtMs = 10_000_000 }},
		{name: "sign-in with a person", change: func(r *passkeystore.ChallengeRow) { r.Kind = "sign-in" }},
		{name: "fresh with no session", change: func(r *passkeystore.ChallengeRow) { r.SessionID = "" }},
		{name: "person with a quote", change: func(r *passkeystore.ChallengeRow) { r.PrincipalID = "prn-1'" }},
	}

	for _, c := range cases {
		r := good
		c.change(&r)

		if _, err := passkeystore.ChallengeOf(r); !errors.Is(err, mfa.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", c.name, err)
		}
	}
}

// looseTx is a Tx whose delete ignores the person, as a changed adapter
// could. Each other method answers for one person prn-1 with no local user
// and two live passkeys of prn-2.
type looseTx struct {
	deleted *int
}

func (looseTx) LockPrincipal(context.Context, string) (passkeystore.Principal, error) {
	return passkeystore.Principal{Kind: string(principal.KindPerson), BreakGlass: false}, nil
}

func (looseTx) LocalUserOf(context.Context, string) (passkeystore.LocalUser, error) {
	return passkeystore.LocalUser{PasswordHash: "", Demo: false}, passkeystore.ErrNoRow
}

func (looseTx) CountPasskeys(context.Context, string) (int64, error) { return 2, nil }

func (looseTx) CountLivePasskeys(context.Context, string) (int64, error) { return 2, nil }

func (looseTx) PasskeyByID(context.Context, string) (passkeystore.Row, error) {
	r := row()
	r.PrincipalID = "prn-2"

	return r, nil
}

func (looseTx) PasskeyByCredentialID(context.Context, []byte) (passkeystore.Row, error) {
	return passkeystore.Row{}, passkeystore.ErrNoRow
}

func (looseTx) ListPasskeys(context.Context, string) ([]passkeystore.Row, error) { return nil, nil }

func (looseTx) CreatePasskey(context.Context, passkeystore.Row) (int64, error) { return 0, nil }

func (t looseTx) DeletePasskey(context.Context, string, string) (int64, error) {
	*t.deleted++

	return 1, nil
}

func (looseTx) RecordUse(context.Context, string, int64, int64) (int64, error) { return 0, nil }

func (looseTx) Suspend(context.Context, string, int64) (int64, error) { return 0, nil }

func (looseTx) CreateChallenge(context.Context, passkeystore.ChallengeRow) (int64, error) {
	return 0, nil
}

func (looseTx) DeleteExpiredChallenges(context.Context, int64) (int64, error) { return 0, nil }

func (looseTx) CountOpenChallenges(context.Context, string, int64) (int64, error) { return 0, nil }

func (looseTx) LockSignInChallenges(context.Context) error { return nil }

func (looseTx) CountOpenChallengesBySourceGroup(context.Context, string, string, int64) (int64, error) {
	return 0, nil
}

func (looseTx) CountOpenChallengesBySource(context.Context, string, string, int64) (int64, error) {
	return 0, nil
}

func (looseTx) TakeChallenge(context.Context, string) (passkeystore.ChallengeRow, error) {
	return passkeystore.ChallengeRow{}, passkeystore.ErrNoRow
}

// TestRemoveChecksTheOwnerItself checks the owner check of a removal alone:
// the delete of the Tx ignores the person, so only the check of the row
// stops a removal of a passkey of another person.
func TestRemoveChecksTheOwnerItself(t *testing.T) {
	t.Parallel()

	deleted := 0

	err := passkeystore.RemovePasskey(t.Context(), looseTx{deleted: &deleted}, "prn-1", "pky-1")
	if !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("remove a passkey of another person: err = %v, want ErrNotFound", err)
	}

	if deleted != 0 {
		t.Fatalf("deletes = %d, want 0", deleted)
	}
}
