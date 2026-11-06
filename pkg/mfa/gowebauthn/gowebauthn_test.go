package gowebauthn_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
)

const (
	externalURL = "https://dustpan.example.com/console"
	rpID        = "dustpan.example.com"
	origin      = "https://dustpan.example.com"
)

// person is the person of the tests.
func person() mfa.Person {
	return mfa.Person{ID: "prn-alice", Name: "alice", DisplayName: "Alice"}
}

// newRP returns the relying party of externalURL.
func newRP(t *testing.T) *gowebauthn.RelyingParty {
	t.Helper()

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("new relying party: %v", err)
	}

	return rp
}

// challenge returns a new challenge of mfa.ChallengeBytes.
func challenge(t *testing.T) []byte {
	t.Helper()

	c := make([]byte, mfa.ChallengeBytes)
	if _, err := rand.Read(c); err != nil {
		t.Fatalf("challenge: %v", err)
	}

	return c
}

// enrolled registers a new credential of person with the relying party and
// returns the credential and the passkey that the store would hold.
func enrolled(t *testing.T, rp *gowebauthn.RelyingParty, auth *webauthntest.Authenticator) (
	*webauthntest.Credential, mfa.Passkey,
) {
	t.Helper()

	cred, err := webauthntest.NewCredential([]byte(person().ID), true)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}

	c := challenge(t)

	response, err := auth.Register(cred, webauthntest.For(c))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	reg, err := rp.VerifyCreation(person(), c, response)
	if err != nil {
		t.Fatalf("verify creation: %v", err)
	}

	return cred, mfa.Passkey{
		CreatedAt:      time.Now(),
		LastUsedAt:     time.Time{},
		SuspendedAt:    time.Time{},
		ID:             "pky-1",
		PersonID:       person().ID,
		Name:           "laptop",
		CredentialID:   reg.CredentialID,
		PublicKey:      reg.PublicKey,
		Transports:     reg.Transports,
		AAGUID:         reg.AAGUID,
		SignCount:      reg.SignCount,
		BackupEligible: reg.BackupEligible,
	}
}

// TestOriginOfTheExternalURL checks that the relying party ID and the
// origin come from the external URL alone.
func TestOriginOfTheExternalURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want gowebauthn.Site
	}{
		{raw: externalURL, want: gowebauthn.Site{RPID: rpID, Origin: origin}},
		{raw: "https://dustpan.example.com:8443", want: gowebauthn.Site{
			RPID: rpID, Origin: "https://dustpan.example.com:8443",
		}},
		{raw: "http://localhost:8080", want: gowebauthn.Site{RPID: "localhost", Origin: "http://localhost:8080"}},
	}

	for _, c := range cases {
		got, err := gowebauthn.Origin(c.raw)
		if err != nil || got != c.want {
			t.Errorf("Origin(%q) = %+v, %v; want %+v", c.raw, got, err, c.want)
		}
	}
}

// TestOriginRefusesBadURLs checks the boundary of the configuration: a URL
// that a browser cannot use for WebAuthn fails, in Origin and in New.
func TestOriginRefusesBadURLs(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"http://dustpan.example.com", "https://192.0.2.1", "https://admin@dustpan.example.com",
		"https://dustpan.example.com/?a=1", "https://dustpan.example.com/#x", "dustpan.example.com",
		"ftp://dustpan.example.com", "", "https://Dustpan.Example.com", "https://dustpan.example.com\r\nX: y",
		"https://[::1]", "https://dustpan..example.com",
	} {
		if _, err := gowebauthn.Origin(raw); !errors.Is(err, gowebauthn.ErrConfig) {
			t.Errorf("Origin(%q) err = %v, want ErrConfig", raw, err)
		}

		if _, err := gowebauthn.New(raw); !errors.Is(err, gowebauthn.ErrConfig) {
			t.Errorf("New(%q) err = %v, want ErrConfig", raw, err)
		}
	}
}

// TestRegisterAndAssert runs a register ceremony and then an assertion of
// each kind with the software authenticator.
func TestRegisterAndAssert(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	auth := webauthntest.New(rpID, origin)
	cred, key := enrolled(t, rp, auth)

	if !key.BackupEligible || len(key.Transports) != 2 {
		t.Fatalf("registration = %+v, want backup eligible with two transports", key)
	}

	for _, k := range []mfa.Ceremony{mfa.CeremonySignIn, mfa.CeremonySecondFactor, mfa.CeremonyFresh} {
		c := challenge(t)

		response, err := auth.Assert(cred, webauthntest.For(c))
		if err != nil {
			t.Fatalf("%s: assert: %v", k, err)
		}

		got, err := rp.VerifyAssertion(k, person(), key, c, response)
		if err != nil {
			t.Fatalf("%s: verify assertion: %v", k, err)
		}

		if got.SignCount != cred.Counter || !got.UserVerified {
			t.Fatalf("%s: assertion = %+v, want counter %d and user verification", k, got, cred.Counter)
		}
	}
}

// TestOptionsCarryTheChallenge checks that the options name the relying
// party, the challenge of the service and the rules of each kind.
func TestOptionsCarryTheChallenge(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	auth := webauthntest.New(rpID, origin)
	_, key := enrolled(t, rp, auth)
	c := challenge(t)

	creation, err := rp.CreationOptions(person(), c, []mfa.Passkey{key})
	if err != nil {
		t.Fatalf("creation options: %v", err)
	}

	got, err := webauthntest.ChallengeOf(creation)
	if err != nil || !bytes.Equal(got, c) {
		t.Fatalf("creation challenge = %x, %v; want %x", got, err, c)
	}

	for _, want := range []string{
		`"id":"` + rpID + `"`, `"residentKey":"required"`, `"userVerification":"required"`,
		`"attestation":"none"`, base64.RawURLEncoding.EncodeToString(key.CredentialID),
	} {
		if !strings.Contains(string(creation), want) {
			t.Errorf("creation options %s hold no %s", creation, want)
		}
	}

	second, err := rp.RequestOptions(mfa.CeremonySecondFactor, c, []mfa.Passkey{key})
	if err != nil {
		t.Fatalf("request options: %v", err)
	}

	if !strings.Contains(string(second), `"userVerification":"preferred"`) {
		t.Errorf("second factor options %s ask for no preferred user verification", second)
	}

	signIn, err := rp.RequestOptions(mfa.CeremonySignIn, c, nil)
	if err != nil {
		t.Fatalf("sign-in options: %v", err)
	}

	if strings.Contains(string(signIn), "allowCredentials") {
		t.Errorf("sign-in options %s name credentials, want a discoverable credential", signIn)
	}
}

// TestOptionsRefuseBadInput checks the boundary of the options: a challenge
// of another size, a person with no ID, a register kind for an assertion and
// an assertion of a known person with no passkey.
func TestOptionsRefuseBadInput(t *testing.T) {
	t.Parallel()

	rp := newRP(t)

	checks := []struct {
		call func() error
		name string
	}{
		{name: "short challenge", call: func() error {
			_, err := rp.CreationOptions(person(), make([]byte, 16), nil)

			return err
		}},
		{name: "no person ID", call: func() error {
			_, err := rp.CreationOptions(mfa.Person{ID: "", Name: "x", DisplayName: ""}, challenge(t), nil)

			return err
		}},
		{name: "long person ID", call: func() error {
			_, err := rp.CreationOptions(mfa.Person{ID: strings.Repeat("p", 65), Name: "x", DisplayName: ""},
				challenge(t), nil)

			return err
		}},
		{name: "register assertion", call: func() error {
			_, err := rp.RequestOptions(mfa.CeremonyRegister, challenge(t), nil)

			return err
		}},
		{name: "second factor with no passkey", call: func() error {
			_, err := rp.RequestOptions(mfa.CeremonySecondFactor, challenge(t), nil)

			return err
		}},
		{name: "long challenge", call: func() error {
			_, err := rp.RequestOptions(mfa.CeremonySignIn, make([]byte, 64), nil)

			return err
		}},
	}

	for _, c := range checks {
		if err := c.call(); !errors.Is(err, mfa.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", c.name, err)
		}
	}
}

// assertInput is the input of one case of TestAssertionChecks: the
// ceremony of the authenticator, its credential, the passkey of the store, the
// kind and the challenge that the service expects.
type assertInput struct {
	ceremony webauthntest.Ceremony
	cred     *webauthntest.Credential
	key      mfa.Passkey
	kind     mfa.Ceremony
	expected []byte
}

// withCeremony returns the change of an input that changes its ceremony.
func withCeremony(change func(webauthntest.Ceremony) webauthntest.Ceremony) func(in *assertInput) {
	return func(in *assertInput) { in.ceremony = change(in.ceremony) }
}

// assertCases returns the cases of TestAssertionChecks. other is a credential
// that the passkey of the store does not hold.
func assertCases(t *testing.T, other *webauthntest.Credential) []struct {
	change func(in *assertInput)
	name   string
} {
	t.Helper()

	signIn := func(in *assertInput) { in.kind = mfa.CeremonySignIn }
	flags := func(f byte) func(in *assertInput) {
		return withCeremony(func(c webauthntest.Ceremony) webauthntest.Ceremony { return c.WithFlags(f) })
	}

	return []struct {
		change func(in *assertInput)
		name   string
	}{
		{name: "another origin", change: withCeremony(func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithOrigin("https://evil.example.com")
		})},
		{name: "a subdomain origin", change: withCeremony(func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithOrigin("https://a.dustpan.example.com")
		})},
		{name: "an http origin", change: withCeremony(func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithOrigin("http://dustpan.example.com")
		})},
		{name: "another relying party", change: withCeremony(func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithRPID("evil.example.com")
		})},
		{name: "a create type", change: withCeremony(func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithType("webauthn.create")
		})},
		{name: "another challenge", change: func(in *assertInput) { in.expected = challenge(t) }},
		{name: "a bad signature", change: withCeremony(webauthntest.Ceremony.WithBadSignature)},
		{name: "no user presence", change: flags(webauthntest.FlagUserVerified | webauthntest.FlagBackupEligible)},
		{name: "no user verification on a sign-in", change: func(in *assertInput) {
			signIn(in)
			flags(webauthntest.FlagUserPresent | webauthntest.FlagBackupEligible)(in)
		}},
		{
			name:   "a changed backup eligibility",
			change: flags(webauthntest.FlagUserPresent | webauthntest.FlagUserVerified),
		},
		{name: "the key of another credential", change: func(in *assertInput) { in.cred = other }},
		{name: "a user handle of another person", change: func(in *assertInput) {
			signIn(in)
			in.ceremony = in.ceremony.WithUserHandle([]byte("prn-mallory"))
		}},
		{name: "a passkey of another person", change: func(in *assertInput) { in.key.PersonID = "prn-mallory" }},
		{name: "a cross-origin frame", change: withCeremony(webauthntest.Ceremony.WithCrossOrigin)},
	}
}

// TestAssertionChecks sends a response that fails one check at a time. Each
// check stops the assertion alone.
func TestAssertionChecks(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	auth := webauthntest.New(rpID, origin)
	cred, key := enrolled(t, rp, auth)

	other, err := webauthntest.NewCredential([]byte(person().ID), true)
	if err != nil {
		t.Fatalf("other credential: %v", err)
	}

	for _, c := range assertCases(t, other) {
		in := assertInput{
			ceremony: webauthntest.For(challenge(t)), cred: cred, key: key, kind: mfa.CeremonyFresh,
			expected: nil,
		}
		in.expected = in.ceremony.Challenge
		c.change(&in)

		response, err := auth.Assert(in.cred, in.ceremony)
		if err != nil {
			t.Fatalf("%s: assert: %v", c.name, err)
		}

		_, err = rp.VerifyAssertion(in.kind, person(), in.key, in.expected, response)
		if !errors.Is(err, mfa.ErrVerification) {
			t.Errorf("%s: err = %v, want ErrVerification", c.name, err)
		}
	}
}

// TestSecondFactorAcceptsNoUserVerification checks that a second factor
// prefers user verification and does not require it.
func TestSecondFactorAcceptsNoUserVerification(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	auth := webauthntest.New(rpID, origin)
	cred, key := enrolled(t, rp, auth)
	noUV := webauthntest.FlagUserPresent | webauthntest.FlagBackupEligible | webauthntest.FlagBackupState
	c := challenge(t)

	response, err := auth.Assert(cred, webauthntest.For(c).WithFlags(noUV))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	got, err := rp.VerifyAssertion(mfa.CeremonySecondFactor, person(), key, c, response)
	if err != nil || got.UserVerified {
		t.Fatalf("second factor with no user verification = %+v, %v; want a pass without it", got, err)
	}

	c = challenge(t)

	response, err = auth.Assert(cred, webauthntest.For(c).WithFlags(noUV))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	_, err = rp.VerifyAssertion(mfa.CeremonyFresh, person(), key, c, response)
	if !errors.Is(err, mfa.ErrVerification) {
		t.Fatalf("fresh step with no user verification: err = %v, want ErrVerification", err)
	}
}

// TestCreationChecks sends a registration response that fails one check at
// a time.
func TestCreationChecks(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	auth := webauthntest.New(rpID, origin)

	cases := []struct {
		change func(c webauthntest.Ceremony) webauthntest.Ceremony
		name   string
	}{
		{name: "another origin", change: func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithOrigin("https://evil.example.com")
		}},
		{name: "another relying party", change: func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithRPID("evil.example.com")
		}},
		{name: "a get type", change: func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithType("webauthn.get")
		}},
		{name: "another challenge", change: func(webauthntest.Ceremony) webauthntest.Ceremony {
			return webauthntest.For(challenge(t))
		}},
		{name: "no user verification", change: func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithFlags(webauthntest.FlagUserPresent | webauthntest.FlagAttestedData)
		}},
		{name: "a backup state with no eligibility", change: func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithFlags(webauthntest.FlagUserPresent | webauthntest.FlagUserVerified |
				webauthntest.FlagBackupState | webauthntest.FlagAttestedData)
		}},
	}

	for _, c := range cases {
		cred, err := webauthntest.NewCredential([]byte(person().ID), false)
		if err != nil {
			t.Fatalf("credential: %v", err)
		}

		expected := challenge(t)

		response, err := auth.Register(cred, c.change(webauthntest.For(expected)))
		if err != nil {
			t.Fatalf("%s: register: %v", c.name, err)
		}

		if _, err := rp.VerifyCreation(person(), expected, response); !errors.Is(err, mfa.ErrVerification) {
			t.Errorf("%s: err = %v, want ErrVerification", c.name, err)
		}
	}
}

// TestCreationRefusesAttestation sends registrations with an attestation
// format other than "none". The adapter refuses each one before the module
// parses a statement.
func TestCreationRefusesAttestation(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	auth := webauthntest.New(rpID, origin)

	for _, format := range []string{"packed", "tpm", "android-safetynet", "android-key", "fido-u2f", "apple", "x"} {
		cred, err := webauthntest.NewCredential([]byte(person().ID), false)
		if err != nil {
			t.Fatalf("credential: %v", err)
		}

		c := challenge(t)

		response, err := auth.Register(cred, webauthntest.For(c).WithFormat(format))
		if err != nil {
			t.Fatalf("%s: register: %v", format, err)
		}

		_, err = rp.VerifyCreation(person(), c, response)
		if !errors.Is(err, mfa.ErrVerification) || !strings.Contains(err.Error(), "attestation format") {
			t.Errorf("%s: err = %v, want ErrVerification for the attestation format", format, err)
		}
	}
}

// TestHeadRefusesBadJSON checks the boundary of the response: size, JSON,
// base64url and the size of each value.
func TestHeadRefusesBadJSON(t *testing.T) {
	t.Parallel()

	rp := newRP(t)
	enc := base64.RawURLEncoding

	body := func(rawID, clientData, handle string) []byte {
		out, err := json.Marshal(map[string]any{
			"rawId":    rawID,
			"response": map[string]string{"clientDataJSON": clientData, "userHandle": handle},
		})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		return out
	}

	cd := func(challenge string) string {
		return enc.EncodeToString([]byte(`{"challenge":"` + challenge + `"}`))
	}

	good := enc.EncodeToString(make([]byte, mfa.ChallengeBytes))

	cases := []struct {
		name     string
		response []byte
	}{
		{name: "empty", response: nil},
		{name: "too long", response: bytes.Repeat([]byte(" "), mfa.MaxResponseBytes+1)},
		{name: "not JSON", response: []byte("{")},
		{name: "a JSON array", response: []byte("[]")},
		{name: "no credential ID", response: body("", cd(good), "")},
		{name: "a credential ID that is not base64url", response: body("a+b/", cd(good), "")},
		{name: "a long credential ID", response: body(enc.EncodeToString(make([]byte, mfa.MaxCredentialIDBytes+1)),
			cd(good), "")},
		{name: "client data that is not base64url", response: body("YQ", "%%%", "")},
		{name: "client data that is not JSON", response: body("YQ", enc.EncodeToString([]byte("x")), "")},
		{name: "a short challenge", response: body("YQ", cd(enc.EncodeToString(make([]byte, 16))), "")},
		{name: "a challenge that is not base64url", response: body("YQ", cd("@@"), "")},
		{name: "a long user handle", response: body("YQ", cd(good), enc.EncodeToString(make([]byte, 65)))},
	}

	for _, c := range cases {
		if _, err := rp.Head(c.response); !errors.Is(err, mfa.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", c.name, err)
		}
	}

	h, err := rp.Head(body("YQ", cd(good), enc.EncodeToString([]byte("prn-alice"))))
	if err != nil || len(h.Challenge) != mfa.ChallengeBytes || string(h.CredentialID) != "a" ||
		string(h.UserHandle) != "prn-alice" {
		t.Fatalf("head = %+v, %v; want the values of the response", h, err)
	}
}

// fuzzTarget is the relying party, the passkey and the challenge of the
// fuzz test, with the seeds of its corpus.
type fuzzTarget struct {
	rp        *gowebauthn.RelyingParty
	key       mfa.Passkey
	challenge []byte
	seeds     [][]byte
}

// newFuzzTarget registers a passkey and makes the seeds: a registration, an
// assertion and small JSON values.
func newFuzzTarget(f *testing.F) fuzzTarget {
	f.Helper()

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		f.Fatalf("new relying party: %v", err)
	}

	auth := webauthntest.New(rpID, origin)

	cred, err := webauthntest.NewCredential([]byte(person().ID), true)
	if err != nil {
		f.Fatalf("credential: %v", err)
	}

	c := make([]byte, mfa.ChallengeBytes)

	registration, err := auth.Register(cred, webauthntest.For(c))
	if err != nil {
		f.Fatalf("register: %v", err)
	}

	assertion, err := auth.Assert(cred, webauthntest.For(c))
	if err != nil {
		f.Fatalf("assert: %v", err)
	}

	reg, err := rp.VerifyCreation(person(), c, registration)
	if err != nil {
		f.Fatalf("verify creation: %v", err)
	}

	key := mfa.Passkey{
		CreatedAt: time.Now(), LastUsedAt: time.Time{}, SuspendedAt: time.Time{}, ID: "pky-1",
		PersonID: person().ID, Name: "laptop", CredentialID: reg.CredentialID, PublicKey: reg.PublicKey,
		Transports: nil, AAGUID: reg.AAGUID, SignCount: 0, BackupEligible: reg.BackupEligible,
	}

	return fuzzTarget{
		rp: rp, key: key, challenge: c,
		seeds: [][]byte{registration, assertion, []byte("{}"), []byte(`{"response":{}}`), nil},
	}
}

// known reports whether err is nil or a refusal of the port.
func known(err error) bool {
	return err == nil || errors.Is(err, mfa.ErrInvalid) || errors.Is(err, mfa.ErrVerification)
}

// FuzzCeremonyJSON sends arbitrary bytes to each reader of a ceremony
// response. No input may panic, and each refusal is ErrInvalid or
// ErrVerification.
func FuzzCeremonyJSON(f *testing.F) {
	ft := newFuzzTarget(f)

	for _, seed := range ft.seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, response []byte) {
		if _, err := ft.rp.Head(response); err != nil && !errors.Is(err, mfa.ErrInvalid) {
			t.Fatalf("head: err = %v, want ErrInvalid", err)
		}

		if _, err := ft.rp.VerifyCreation(person(), ft.challenge, response); !known(err) {
			t.Fatalf("verify creation: err = %v", err)
		}

		for _, k := range []mfa.Ceremony{mfa.CeremonySignIn, mfa.CeremonyFresh} {
			if _, err := ft.rp.VerifyAssertion(k, person(), ft.key, ft.challenge, response); !known(err) {
				t.Fatalf("verify assertion: err = %v", err)
			}
		}
	})
}
