// Package webauthntest is a software authenticator for tests. It makes
// registration and assertion responses with an ES256 key, as a browser sends
// them, and can change each field that a check of the relying party reads,
// so that a test can send hostile input to one check at a time.
//
// The authenticator uses attestation "none" and holds its keys in memory. It
// is for tests only: the binary never links it.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Flags of the authenticator data.
const (
	FlagUserPresent    byte = 0x01
	FlagUserVerified   byte = 0x04
	FlagBackupEligible byte = 0x08
	FlagBackupState    byte = 0x10
	FlagAttestedData   byte = 0x40
)

// The COSE values of an ES256 key.
const (
	coseKeyType      = 1
	coseAlgorithm    = 3
	coseCurve        = -1
	coseX            = -2
	coseY            = -3
	coseKeyTypeEC2   = 2
	coseAlgES256     = -7
	coseCurveP256    = 1
	coordinateBytes  = 32
	credentialIDSize = 32
	aaguidBytes      = 16
)

// keyType is the member of the type of the JSON of a response and of the
// client data.
const keyType = "type"

// ErrNoChallenge reports options JSON with no challenge.
var ErrNoChallenge = errors.New("the options have no challenge")

// Credential is one key of the authenticator.
type Credential struct {
	key        *ecdsa.PrivateKey
	ID         []byte
	UserHandle []byte
	AAGUID     [16]byte
	Counter    uint32
	BackupFlag bool
}

// Authenticator makes the responses of the relying party RPID at Origin.
type Authenticator struct {
	RPID   string
	Origin string
}

// New returns an authenticator for the relying party rpID at origin.
func New(rpID, origin string) *Authenticator {
	return &Authenticator{RPID: rpID, Origin: origin}
}

// Ceremony holds the values of one response. Zero values take the value of
// the authenticator: the type of the ceremony, its origin and relying party
// ID, the flags UP and UV, and the next counter.
type Ceremony struct {
	// Challenge is the challenge of the options.
	Challenge []byte
	// Type replaces the type of the client data.
	Type string
	// Origin replaces the origin of the client data.
	Origin string
	// RPID replaces the relying party ID of the hash in the authenticator
	// data.
	RPID string
	// Flags replaces the flags of the authenticator data when SetFlags is
	// true.
	Flags    byte
	SetFlags bool
	// Counter replaces the next counter when SetCounter is true.
	Counter    uint32
	SetCounter bool
	// BadSignature changes one byte of the signature.
	BadSignature bool
	// UserHandle replaces the user handle of an assertion when not nil.
	UserHandle []byte
	// CrossOrigin sets crossOrigin in the client data.
	CrossOrigin bool
}

// For returns the ceremony of the challenge c with the values of the
// authenticator. The With methods change one value each.
func For(c []byte) Ceremony {
	return Ceremony{
		Challenge: append([]byte(nil), c...), Type: "", Origin: "", RPID: "", Flags: 0, SetFlags: false,
		Counter: 0, SetCounter: false, BadSignature: false, UserHandle: nil, CrossOrigin: false,
	}
}

// WithType returns c with the type t in the client data.
func (c Ceremony) WithType(t string) Ceremony {
	c.Type = t

	return c
}

// WithOrigin returns c with the origin o in the client data.
func (c Ceremony) WithOrigin(o string) Ceremony {
	c.Origin = o

	return c
}

// WithRPID returns c with the hash of the relying party ID id.
func (c Ceremony) WithRPID(id string) Ceremony {
	c.RPID = id

	return c
}

// WithFlags returns c with the flags f in the authenticator data.
func (c Ceremony) WithFlags(f byte) Ceremony {
	c.Flags, c.SetFlags = f, true

	return c
}

// WithCounter returns c with the counter n.
func (c Ceremony) WithCounter(n uint32) Ceremony {
	c.Counter, c.SetCounter = n, true

	return c
}

// WithBadSignature returns c with one byte of the signature changed.
func (c Ceremony) WithBadSignature() Ceremony {
	c.BadSignature = true

	return c
}

// WithUserHandle returns c with the user handle h.
func (c Ceremony) WithUserHandle(h []byte) Ceremony {
	c.UserHandle = h

	return c
}

// WithCrossOrigin returns c with crossOrigin set in the client data.
func (c Ceremony) WithCrossOrigin() Ceremony {
	c.CrossOrigin = true

	return c
}

// ChallengeOf returns the challenge of the options JSON of a register or an
// assertion ceremony.
func ChallengeOf(options []byte) ([]byte, error) {
	var o map[string]map[string]any

	if err := json.Unmarshal(options, &o); err != nil {
		return nil, fmt.Errorf("decode options: %w", err)
	}

	text, ok := o["publicKey"]["challenge"].(string)
	if !ok || text == "" {
		return nil, ErrNoChallenge
	}

	c, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("decode challenge: %w", err)
	}

	return c, nil
}

// NewCredential returns a new key for the user handle userHandle.
func NewCredential(userHandle []byte, backupEligible bool) (*Credential, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("make key: %w", err)
	}

	id := make([]byte, credentialIDSize)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("make credential ID: %w", err)
	}

	return &Credential{
		key: key, ID: id, UserHandle: append([]byte(nil), userHandle...),
		AAGUID: [aaguidBytes]byte{}, Counter: 0, BackupFlag: backupEligible,
	}, nil
}

// defaultFlags returns the flags UP and UV, and BE and BS for a backup
// eligible credential.
func defaultFlags(cred *Credential) byte {
	flags := FlagUserPresent | FlagUserVerified
	if cred.BackupFlag {
		flags |= FlagBackupEligible | FlagBackupState
	}

	return flags
}

// nextCounter sets the counter of cred for the ceremony c.
func nextCounter(cred *Credential, c Ceremony) {
	if c.SetCounter {
		cred.Counter = c.Counter

		return
	}

	cred.Counter++
}

// Register returns the registration response JSON of cred for the
// ceremony c, with the transports internal and hybrid.
func (a *Authenticator) Register(cred *Credential, c Ceremony) ([]byte, error) {
	if c.SetCounter {
		cred.Counter = c.Counter
	}

	key, err := coseKey(&cred.key.PublicKey)
	if err != nil {
		return nil, err
	}

	data, err := a.clientData(c, "webauthn.create")
	if err != nil {
		return nil, err
	}

	auth := a.authData(cred, c, defaultFlags(cred)|FlagAttestedData)
	auth = append(auth, cred.AAGUID[:]...)
	auth = binary.BigEndian.AppendUint16(auth, credentialIDSize)
	auth = append(auth, cred.ID...)
	auth = append(auth, key...)

	attestation := cborMap(
		cborText("fmt"), cborText("none"),
		cborText("attStmt"), cborMap(),
		cborText("authData"), cborBytes(auth),
	)

	enc := base64.RawURLEncoding

	out, err := json.Marshal(map[string]any{
		"id":    enc.EncodeToString(cred.ID),
		"rawId": enc.EncodeToString(cred.ID),
		keyType: "public-key",
		"response": map[string]any{
			"clientDataJSON":    enc.EncodeToString(data),
			"attestationObject": enc.EncodeToString(attestation),
			"transports":        []string{"internal", "hybrid"},
		},
		"clientExtensionResults": map[string]any{},
	})
	if err != nil {
		return nil, fmt.Errorf("encode registration: %w", err)
	}

	return out, nil
}

// Assert returns the assertion response JSON of cred for the ceremony c. It
// adds 1 to the counter of cred unless c sets it.
func (a *Authenticator) Assert(cred *Credential, c Ceremony) ([]byte, error) {
	nextCounter(cred, c)

	data, err := a.clientData(c, "webauthn.get")
	if err != nil {
		return nil, err
	}

	auth := a.authData(cred, c, defaultFlags(cred))
	digest := sha256.Sum256(data)
	signed := sha256.Sum256(append(append([]byte(nil), auth...), digest[:]...))

	sig, err := ecdsa.SignASN1(rand.Reader, cred.key, signed[:])
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	if c.BadSignature {
		sig[len(sig)-1] ^= 0x01
	}

	handle := cred.UserHandle
	if c.UserHandle != nil {
		handle = c.UserHandle
	}

	enc := base64.RawURLEncoding

	out, err := json.Marshal(map[string]any{
		"id":    enc.EncodeToString(cred.ID),
		"rawId": enc.EncodeToString(cred.ID),
		keyType: "public-key",
		"response": map[string]any{
			"clientDataJSON":    enc.EncodeToString(data),
			"authenticatorData": enc.EncodeToString(auth),
			"signature":         enc.EncodeToString(sig),
			"userHandle":        enc.EncodeToString(handle),
		},
		"clientExtensionResults": map[string]any{},
	})
	if err != nil {
		return nil, fmt.Errorf("encode assertion: %w", err)
	}

	return out, nil
}

// clientData returns the client data JSON of c with the default type.
func (a *Authenticator) clientData(c Ceremony, defaultType string) ([]byte, error) {
	typ := c.Type
	if typ == "" {
		typ = defaultType
	}

	origin := c.Origin
	if origin == "" {
		origin = a.Origin
	}

	data, err := json.Marshal(map[string]any{
		keyType:       typ,
		"challenge":   base64.RawURLEncoding.EncodeToString(c.Challenge),
		"origin":      origin,
		"crossOrigin": c.CrossOrigin,
	})
	if err != nil {
		return nil, fmt.Errorf("encode client data: %w", err)
	}

	return data, nil
}

// authData returns the authenticator data of c without attested data.
func (a *Authenticator) authData(cred *Credential, c Ceremony, flags byte) []byte {
	rpID := c.RPID
	if rpID == "" {
		rpID = a.RPID
	}

	hash := sha256.Sum256([]byte(rpID))

	if c.SetFlags {
		flags = c.Flags
	}

	out := append([]byte(nil), hash[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, cred.Counter)

	return out
}

// coseKey returns the COSE key of the P-256 public key k.
func coseKey(k *ecdsa.PublicKey) ([]byte, error) {
	raw, err := k.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encode public key: %w", err)
	}

	x := raw[1 : 1+coordinateBytes]
	y := raw[1+coordinateBytes:]

	return cborMap(
		cborInt(coseKeyType), cborInt(coseKeyTypeEC2),
		cborInt(coseAlgorithm), cborInt(coseAlgES256),
		cborInt(coseCurve), cborInt(coseCurveP256),
		cborInt(coseX), cborBytes(x),
		cborInt(coseY), cborBytes(y),
	), nil
}
