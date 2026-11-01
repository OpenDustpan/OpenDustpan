package stepuptest

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/stepup"
)

// Vector is a signature of a key over a message for a scope.
type Vector struct {
	Key       stepup.Key
	Scope     stepup.Scope
	Message   []byte
	Signature []byte
}

// VerifierHarness is a verifier under test and the vectors of its key
// format. The suite runs the cases of the factor Factor and of every
// factor. It skips the cases of the other factors. A case of the factor
// Factor whose vector is nil fails, so that an adapter cannot skip a case.
type VerifierHarness struct {
	// Verifier is the verifier under test.
	Verifier stepup.Verifier
	// Factor is stepup.FactorSSH or stepup.FactorOpenPGP.
	Factor stepup.Factor
	// Write is a valid signature of ScopeWrite by a key that is not a FIDO
	// key.
	Write *Vector
	// KeyAdd is a valid signature of ScopeKeyAdd.
	KeyAdd *Vector
	// OtherKey is a key of the type of Write.Key that did not sign Write.
	OtherKey *stepup.Key
	// OtherMessage is a message other than Write.Message, which Write does
	// not sign.
	OtherMessage []byte
	// DisallowedType is a valid signature by a key whose type is outside
	// the allowlist.
	DisallowedType *Vector
	// SmallRSA is a valid signature by an RSA key below 3072 bits.
	SmallRSA *Vector

	// FIDO is a valid signature of ScopeWrite by an sk- key, and
	// FIDOAssertion is its assertion. SSH only.
	FIDO          *Vector
	FIDOAssertion stepup.Assertion
	// FIDONoPresence is a signature by an sk- key without user presence.
	// SSH only.
	FIDONoPresence *Vector
	// OtherNamespace is a signature by Write.Key with another SSH namespace.
	// SSH only.
	OtherNamespace *Vector
	// OtherHash is a signature with a hash algorithm other than sha256 or
	// sha512, which verifies with its key apart from that. SSH only.
	OtherHash *Vector

	// NoNotation is an OpenPGP signature without the notation. OpenPGP only.
	NoNotation *Vector
	// UnhashedNotation is an OpenPGP signature with the notation in the
	// unhashed area. OpenPGP only.
	UnhashedNotation *Vector
	// TextMode is a text-mode OpenPGP signature. OpenPGP only.
	TextMode *Vector
	// OtherIssuer is an OpenPGP signature whose issuer fingerprint is not
	// the key or a signing subkey of the key. OpenPGP only.
	OtherIssuer *Vector
	// ExpiredOrRevoked are OpenPGP signatures by an expired or revoked key
	// or subkey. OpenPGP only.
	ExpiredOrRevoked []Vector
}

// VerifierFactory returns the harness for one subtest.
type VerifierFactory func(t *testing.T) VerifierHarness

// verifierCase is one case of the Verifier suite. factor is the factor of
// the case, or empty for a case of every factor.
type verifierCase struct {
	name   string
	factor stepup.Factor
	run    func(t *testing.T, h VerifierHarness)
}

// verifierCases returns the cases of the Verifier suite, in the order of the
// run.
func verifierCases() []verifierCase {
	return []verifierCase{
		{"a valid SSH signature returns its assertion", stepup.FactorSSH, validSignature},
		{"a valid sk- signature returns its user presence, user verification and counter", stepup.FactorSSH, validFIDO},
		{"an SSH signature with another namespace fails with ErrBadAnswer", stepup.FactorSSH, otherNamespace},
		{"a key-add signature never verifies for a write or a window", "", keyAddNotWrite},
		{"a write signature never verifies for a key add", "", writeNotKeyAdd},
		{
			"an SSH signature with a hash algorithm other than sha256 or sha512 fails with ErrBadAnswer",
			stepup.FactorSSH, otherHash,
		},
		{"an SSH signature whose embedded key is not k fails with ErrBadAnswer", stepup.FactorSSH, otherKey},
		{"an SSH signature over another message fails with ErrBadAnswer", "", otherMessage},
		{"a key type outside the allowlist fails with ErrInvalid", "", disallowedType},
		{"an RSA key below 3072 bits fails with ErrInvalid", "", smallRSA},
		{"an sk- signature without user presence fails with ErrBadAnswer", stepup.FactorSSH, fidoNoPresence},
		{"a valid OpenPGP signature returns its assertion", stepup.FactorOpenPGP, validSignature},
		{"an OpenPGP signature without the notation fails with ErrBadAnswer", stepup.FactorOpenPGP, noNotation},
		{
			"an OpenPGP signature with the notation in the unhashed area fails with ErrBadAnswer",
			stepup.FactorOpenPGP, unhashedNotation,
		},
		{"a text-mode OpenPGP signature fails with ErrBadAnswer", stepup.FactorOpenPGP, textMode},
		{
			"an OpenPGP signature whose issuer fingerprint is not k or a signing subkey of k fails with ErrBadAnswer",
			stepup.FactorOpenPGP, otherIssuer,
		},
		{
			"an OpenPGP signature by an expired or revoked key or subkey fails with ErrBadAnswer",
			stepup.FactorOpenPGP, expiredOrRevoked,
		},
		{"a malformed signature fails with ErrBadAnswer and does not panic", "", malformed},
	}
}

// VerifierCaseNames returns the names of the cases of the Verifier suite,
// in the order of the run.
func VerifierCaseNames() []string {
	cases := verifierCases()
	names := make([]string, 0, len(cases))

	for _, c := range cases {
		names = append(names, c.name)
	}

	return names
}

// RunVerifier runs every case of the Verifier suite against the harnesses
// that newHarness returns. Each Verifier adapter runs it:
//
//	func TestVerifierConformance(t *testing.T) {
//		t.Parallel()
//		stepuptest.RunVerifier(t, func(t *testing.T) stepuptest.VerifierHarness { return harness(t) })
//	}
func RunVerifier(t *testing.T, newHarness VerifierFactory) {
	t.Helper()

	for _, c := range verifierCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if newHarness == nil {
				t.Fatal("stepuptest.RunVerifier needs a harness factory")
			}

			h := newHarness(t)
			if h.Verifier == nil {
				t.Fatal("the harness has no verifier")
			}

			if h.Factor != stepup.FactorSSH && h.Factor != stepup.FactorOpenPGP {
				t.Fatalf("the harness factor %q is not a key factor", h.Factor)
			}

			if c.factor != "" && c.factor != h.Factor {
				t.Skipf("the case is for the factor %q, and the harness is for %q", c.factor, h.Factor)
			}

			c.run(t, h)
		})
	}
}

// noAssertion is the assertion of a key that is not a FIDO key, and of an
// error.
func noAssertion() stepup.Assertion {
	return stepup.Assertion{UserPresent: false, UserVerified: false, Counter: 0}
}

// with returns a copy of v with another scope and another message.
func with(v Vector, scope stepup.Scope, message []byte) Vector {
	v.Scope = scope
	v.Message = message

	return v
}

// need fails the case when the harness has no vector for it.
func need[T any](t *testing.T, v *T, field string) *T {
	t.Helper()

	if v == nil {
		t.Fatalf("the harness has no %s", field)
	}

	return v
}

// verify calls the verifier with the vector, the scope and the message.
func verify(t *testing.T, h VerifierHarness, v Vector, scope stepup.Scope, message []byte) (stepup.Assertion, error) {
	t.Helper()

	return h.Verifier.Verify(t.Context(), v.Key, scope, message, v.Signature)
}

// wantValid checks that the vector verifies for its scope with the
// assertion want.
func wantValid(t *testing.T, h VerifierHarness, v Vector, want stepup.Assertion) {
	t.Helper()

	got, err := verify(t, h, v, v.Scope, v.Message)
	if err != nil {
		t.Fatalf("Verify of a valid signature: %v", err)
	}

	if got != want {
		t.Fatalf("Verify assertion = %+v, want %+v", got, want)
	}
}

// wantError checks that the vector fails with want.
func wantError(t *testing.T, h VerifierHarness, v Vector, want error) {
	t.Helper()

	got, err := verify(t, h, v, v.Scope, v.Message)
	if !errors.Is(err, want) {
		t.Fatalf("Verify error = %v, want %v", err, want)
	}

	if got != noAssertion() {
		t.Fatalf("Verify returned the assertion %+v with an error", got)
	}
}

func validSignature(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.Write, "Write vector")
	if v.Key.Factor != h.Factor {
		t.Fatalf("the Write key has the factor %q, want %q", v.Key.Factor, h.Factor)
	}

	wantValid(t, h, v, noAssertion())

	// A window uses the namespace or the notation of a write.
	if _, err := verify(t, h, v, stepup.ScopeWindow, v.Message); err != nil {
		t.Fatalf("Verify of a write signature for a window: %v", err)
	}
}

func validFIDO(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.FIDO, "FIDO vector")
	if !h.FIDOAssertion.UserPresent {
		t.Fatal("the harness FIDOAssertion has no user presence")
	}

	wantValid(t, h, v, h.FIDOAssertion)
}

func otherNamespace(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.OtherNamespace, "OtherNamespace vector")
	for _, scope := range []stepup.Scope{stepup.ScopeWrite, stepup.ScopeWindow, stepup.ScopeKeyAdd} {
		wantError(t, h, with(v, scope, v.Message), stepup.ErrBadAnswer)
	}
}

func keyAddNotWrite(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.KeyAdd, "KeyAdd vector")
	wantValid(t, h, v, noAssertion())
	wantError(t, h, with(v, stepup.ScopeWrite, v.Message), stepup.ErrBadAnswer)
	wantError(t, h, with(v, stepup.ScopeWindow, v.Message), stepup.ErrBadAnswer)
}

func writeNotKeyAdd(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.Write, "Write vector")
	wantError(t, h, with(v, stepup.ScopeKeyAdd, v.Message), stepup.ErrBadAnswer)
}

func otherHash(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.OtherHash, "OtherHash vector")
	wantError(t, h, v, stepup.ErrBadAnswer)
}

func otherKey(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.Write, "Write vector")
	v.Key = *need(t, h.OtherKey, "OtherKey")

	if v.Key.Type != h.Write.Key.Type {
		t.Fatalf("OtherKey has the type %q, want %q", v.Key.Type, h.Write.Key.Type)
	}

	wantError(t, h, v, stepup.ErrBadAnswer)
}

func otherMessage(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.Write, "Write vector")
	if len(h.OtherMessage) == 0 || bytes.Equal(h.OtherMessage, v.Message) {
		t.Fatal("the harness has no OtherMessage that differs from the Write message")
	}

	appended := append(bytes.Clone(v.Message), '\n')
	flipped := bytes.Clone(v.Message)
	flipped[len(flipped)-1] ^= 0x01

	for _, m := range [][]byte{h.OtherMessage, appended, flipped, nil} {
		wantError(t, h, with(v, v.Scope, m), stepup.ErrBadAnswer)
	}
}

func disallowedType(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.DisallowedType, "DisallowedType vector")
	wantError(t, h, v, stepup.ErrInvalid)

	// A key whose stored type is outside the allowlist fails before the
	// signature, whatever the key bytes are.
	w := *need(t, h.Write, "Write vector")
	w.Key.Type = "ssh-dss"
	wantError(t, h, w, stepup.ErrInvalid)

	// A key of another factor is not a key of this verifier.
	w = *h.Write
	w.Key.Factor = stepup.FactorTOTP
	wantError(t, h, w, stepup.ErrInvalid)
}

func smallRSA(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.SmallRSA, "SmallRSA vector")
	wantError(t, h, v, stepup.ErrInvalid)
}

func fidoNoPresence(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.FIDONoPresence, "FIDONoPresence vector")
	wantError(t, h, v, stepup.ErrBadAnswer)
}

func noNotation(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.NoNotation, "NoNotation vector")
	wantError(t, h, v, stepup.ErrBadAnswer)
}

func unhashedNotation(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.UnhashedNotation, "UnhashedNotation vector")
	wantError(t, h, v, stepup.ErrBadAnswer)
}

func textMode(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.TextMode, "TextMode vector")
	wantError(t, h, v, stepup.ErrBadAnswer)
}

func otherIssuer(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.OtherIssuer, "OtherIssuer vector")
	wantError(t, h, v, stepup.ErrBadAnswer)
}

func expiredOrRevoked(t *testing.T, h VerifierHarness) {
	t.Helper()

	if len(h.ExpiredOrRevoked) == 0 {
		t.Fatal("the harness has no ExpiredOrRevoked vector")
	}

	for i := range h.ExpiredOrRevoked {
		wantError(t, h, h.ExpiredOrRevoked[i], stepup.ErrBadAnswer)
	}
}

// malformed sends each prefix of a valid signature, the signature with
// bytes after it, and bytes that are not a signature. Each fails with
// ErrBadAnswer. A panic fails the case.
func malformed(t *testing.T, h VerifierHarness) {
	t.Helper()

	v := *need(t, h.Write, "Write vector")
	const (
		zero       byte = 0x00
		ones       byte = 0xff
		hugeBytes       = 1 << 16
		extraCases      = 8
	)

	sigs := make([][]byte, 0, len(v.Signature)+extraCases)
	sigs = append(sigs, nil, []byte{}, []byte{zero}, []byte("not a signature"), bytes.Repeat([]byte{ones}, hugeBytes))

	for n := range len(v.Signature) {
		sigs = append(sigs, bytes.Clone(v.Signature[:n]))
	}

	sigs = append(sigs,
		append(bytes.Clone(v.Signature), 'x'),
		append(bytes.Clone(v.Signature), zero),
		append(bytes.Clone(v.Signature), v.Signature...),
	)

	for _, sig := range sigs {
		m := v
		m.Signature = sig
		wantError(t, h, m, stepup.ErrBadAnswer)
	}
}
