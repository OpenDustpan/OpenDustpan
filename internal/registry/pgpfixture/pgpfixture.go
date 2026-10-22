// Package pgpfixture makes OpenPGP signing keys and detached signatures for the
// tests of the registry. It uses the same library as the verifier, so a test
// builds a release the way a build tool does. It is a test helper: no
// production code imports it.
package pgpfixture

import (
	"bytes"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/pkg/registry"
)

// Signer is a signing key pair and the public key that a namespace registers.
type Signer struct {
	Entity *openpgp.Entity
	Key    registry.TrustedKey
	// at is the time of the key and of each signature that it makes, or zero
	// for the current time.
	at time.Time
}

// KeySpec says how to make a key. The zero value makes an Ed25519 key that does
// not expire.
type KeySpec struct {
	// RSABits makes an RSA key of this size when above zero.
	RSABits int
	// LifetimeSecs is the lifetime of the key in seconds, or zero for none.
	LifetimeSecs uint32
	// CreatedAt is the creation time of the key, and the time of each
	// signature that the key makes, or zero for the current time. A test with
	// a fixed clock sets it, so that the key is never newer than the clock.
	CreatedAt time.Time
}

// config returns the packet configuration of spec.
func (k KeySpec) config() *packet.Config {
	//nolint:exhaustruct_v5 // The library takes its default for every field that is left out.
	cfg := &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, KeyLifetimeSecs: k.LifetimeSecs}
	if k.RSABits > 0 {
		cfg.Algorithm, cfg.RSABits = packet.PubKeyAlgoRSA, k.RSABits
	}

	cfg.Time = timeFunc(k.CreatedAt)

	return cfg
}

// timeFunc returns the clock of the library for at. A zero at gives nil, which
// is the current time.
func timeFunc(at time.Time) func() time.Time {
	if at.IsZero() {
		return nil
	}

	return func() time.Time { return at }
}

// New makes a key pair as spec says.
func New(tb testing.TB, spec KeySpec) Signer {
	tb.Helper()

	entity, err := openpgp.NewEntity("Release signing", "", "release@example.invalid", spec.config())
	if err != nil {
		tb.Fatalf("make the signing key: %v", err)
	}

	s := Signer{Entity: entity, Key: registry.TrustedKey{Fingerprint: "", Armored: nil}, at: spec.CreatedAt}
	s.Refresh(tb)

	return s
}

// NewSigner makes an Ed25519 key pair that does not expire.
func NewSigner(tb testing.TB) Signer {
	tb.Helper()

	return New(tb, KeySpec{RSABits: 0, LifetimeSecs: 0, CreatedAt: time.Time{}})
}

// NewSignerAt makes an Ed25519 key pair that does not expire, with the creation
// time at. Its signatures carry the time at too.
func NewSignerAt(tb testing.TB, at time.Time) Signer {
	tb.Helper()

	return New(tb, KeySpec{RSABits: 0, LifetimeSecs: 0, CreatedAt: at})
}

// Refresh makes the public key again from the entity, for example after a
// revocation or a new subkey.
func (s *Signer) Refresh(tb testing.TB) {
	tb.Helper()

	s.Key = registry.TrustedKey{
		Fingerprint: Fingerprint(s.Entity),
		Armored:     armorOf(tb, openpgp.PublicKeyType, s.Entity.Serialize),
	}
}

// Fingerprint returns the fingerprint of the primary key of e in uppercase hex.
func Fingerprint(e *openpgp.Entity) string {
	return strings.ToUpper(hex.EncodeToString(e.PrimaryKey.Fingerprint))
}

// PrivateArmor returns the key pair in ASCII armor, as a mistaken upload would
// send it.
func (s *Signer) PrivateArmor(tb testing.TB) []byte {
	tb.Helper()

	return armorOf(tb, openpgp.PrivateKeyType, func(w io.Writer) error {
		return s.Entity.SerializePrivate(w, nil)
	})
}

// Revoke revokes the key and makes the public key again.
func (s *Signer) Revoke(tb testing.TB) {
	tb.Helper()

	if err := s.Entity.RevokeKey(packet.KeyCompromised, "leaked", s.signConfig()); err != nil {
		tb.Fatalf("revoke the key: %v", err)
	}

	s.Refresh(tb)
}

// AddSigningSubkey adds a signing subkey, so that a signature comes from the
// subkey, and makes the public key again.
func (s *Signer) AddSigningSubkey(tb testing.TB) {
	tb.Helper()

	if err := s.Entity.AddSigningSubkey(KeySpec{RSABits: 0, LifetimeSecs: 0, CreatedAt: s.at}.config()); err != nil {
		tb.Fatalf("add a signing subkey: %v", err)
	}

	s.Refresh(tb)
}

// Detach returns the binary detached signature of data.
func (s *Signer) Detach(tb testing.TB, data []byte) []byte {
	tb.Helper()

	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, s.Entity, bytes.NewReader(data), s.signConfig()); err != nil {
		tb.Fatalf("sign: %v", err)
	}

	return sig.Bytes()
}

// DetachArmored returns the detached signature of data in ASCII armor.
func (s *Signer) DetachArmored(tb testing.TB, data []byte) []byte {
	tb.Helper()

	var sig bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&sig, s.Entity, bytes.NewReader(data), s.signConfig()); err != nil {
		tb.Fatalf("sign: %v", err)
	}

	return sig.Bytes()
}

// signConfig returns the packet configuration for a signature or a revocation
// of the key.
func (s *Signer) signConfig() *packet.Config {
	if s.at.IsZero() {
		return nil
	}

	//nolint:exhaustruct_v5 // The library takes its default for every field that is left out.
	return &packet.Config{Time: timeFunc(s.at)}
}

// armorOf writes a key packet set in ASCII armor of kind.
func armorOf(tb testing.TB, kind string, write func(w io.Writer) error) []byte {
	tb.Helper()

	var buf bytes.Buffer

	w, err := armor.Encode(&buf, kind, nil)
	if err != nil {
		tb.Fatalf("start the armor: %v", err)
	}

	if err = write(w); err != nil {
		tb.Fatalf("write the key: %v", err)
	}

	if err = w.Close(); err != nil {
		tb.Fatalf("close the armor: %v", err)
	}

	return buf.Bytes()
}
