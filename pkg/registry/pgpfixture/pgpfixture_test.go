package pgpfixture_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/pkg/registry/pgpfixture"
)

// TestSignerAtUsesTheGivenTime proves that a key made with NewSignerAt, its
// signing subkey and its signatures carry the given time and not the real
// time, so a test with a fixed clock does not depend on the date of the run.
func TestSignerAtUsesTheGivenTime(t *testing.T) {
	t.Parallel()

	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	sg := pgpfixture.NewSignerAt(t, at)

	if got := sg.Entity.PrimaryKey.CreationTime; !got.Equal(at) {
		t.Fatalf("key creation time = %v, want %v", got, at)
	}

	sg.AddSigningSubkey(t)

	for _, sub := range sg.Entity.Subkeys {
		if got := sub.PublicKey.CreationTime; !got.Equal(at) {
			t.Fatalf("subkey creation time = %v, want %v", got, at)
		}
	}

	data := []byte("sums")
	raw := sg.Detach(t, data)

	pkt, err := packet.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read the signature: %v", err)
	}

	sig, ok := pkt.(*packet.Signature)
	if !ok {
		t.Fatalf("packet = %T, want a signature", pkt)
	}

	if !sig.CreationTime.Equal(at) {
		t.Fatalf("signature creation time = %v, want %v", sig.CreationTime, at)
	}

	ring := openpgp.EntityList{sg.Entity}
	if _, err = openpgp.CheckDetachedSignature(ring, bytes.NewReader(data), bytes.NewReader(raw), nil); err != nil {
		t.Fatalf("check the signature: %v", err)
	}
}

// TestSignerWithoutATimeUsesTheCurrentTime proves that NewSigner keeps the
// current time, which the tests of the verifier rely on.
func TestSignerWithoutATimeUsesTheCurrentTime(t *testing.T) {
	t.Parallel()

	before := time.Now().Add(-time.Minute)
	sg := pgpfixture.NewSigner(t)

	if got := sg.Entity.PrimaryKey.CreationTime; got.Before(before) {
		t.Fatalf("key creation time = %v, want the current time", got)
	}
}
