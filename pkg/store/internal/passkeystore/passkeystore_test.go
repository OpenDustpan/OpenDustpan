package passkeystore_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/mfa"
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
		CreatedAtMs: 1_000, ExpiresAtMs: 2_000,
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
