package gowebauthn

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/mfa"
)

// TestDiscoverableHandlerChecksTheResponse calls the handler of a
// discoverable assertion alone, with no check of the module and of the
// service around it. A credential ID or a user handle that the passkey does
// not hold gives no user.
func TestDiscoverableHandlerChecksTheResponse(t *testing.T) {
	t.Parallel()

	owner := mfa.Person{ID: "prn-alice", Name: "alice", DisplayName: "Alice"}
	key := mfa.Passkey{
		CreatedAt: time.Now(), LastUsedAt: time.Time{}, SuspendedAt: time.Time{}, ID: "pky-1", PersonID: owner.ID,
		Name: "laptop", CredentialID: []byte("credential-1"), PublicKey: []byte("key"), Transports: nil,
		AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}
	handler := discoverable(owner, key, user{person: owner, keys: nil})

	cases := []struct {
		name          string
		rawID, handle []byte
	}{
		{name: "another credential ID", rawID: []byte("credential-2"), handle: []byte(owner.ID)},
		{name: "another user handle", rawID: key.CredentialID, handle: []byte("prn-mallory")},
		{name: "no user handle", rawID: key.CredentialID, handle: nil},
		{name: "a prefix of the credential ID", rawID: []byte("credential-"), handle: []byte(owner.ID)},
	}

	for _, c := range cases {
		u, err := handler(c.rawID, c.handle)
		if !errors.Is(err, errNotThePasskey) || u != nil {
			t.Errorf("%s: user = %v, err = %v; want no user and errNotThePasskey", c.name, u, err)
		}
	}

	if u, err := handler(key.CredentialID, []byte(owner.ID)); err != nil || u == nil {
		t.Fatalf("the passkey of the owner: user = %v, err = %v; want the user", u, err)
	}
}
