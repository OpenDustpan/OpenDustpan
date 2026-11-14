package trustrule

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrToken reports a JWT that the exchange refuses. It wraps the reason. The
// reason goes to the audit log, never to the caller.
var ErrToken = errors.New("the token is not valid")

// Limits and tolerances of a JWT.
const (
	// MaxTokenBytes is the longest JWT that the exchange reads.
	MaxTokenBytes = 16 << 10
	// ClockSkew is the tolerance of the exp, nbf and iat claims against the
	// clock of the server, in both directions.
	ClockSkew = 60 * time.Second
	// MaxTokenLifetime is the longest time from now to the exp claim that
	// the exchange accepts. The tokens of CI issuers live 5 to 60 minutes.
	// A token that lives longer is not a token of a workload run.
	MaxTokenLifetime = 24 * time.Hour
	// maxNumericDate bounds a time claim, in seconds: year 5138. A larger
	// value is not a time.
	maxNumericDate = 1e11
)

// headerMembers are the members that a JWT header can hold. A header with
// another member is refused: jku, jwk, x5u and x5c name or hold a key, and
// the key of a token must come from the rule, never from the token. crit
// names extensions that the server does not know.
func headerMembers() []string {
	return []string{"alg", "kid", "typ", "x5t", "x5t#S256"}
}

// Token is a JWT whose signature the server checked. Header holds the alg
// and the kid. Claims holds each member of the payload, as JSON.
type Token struct {
	Claims map[string]json.RawMessage
	Alg    string
	Kid    string
}

// header is the JWT header on the wire.
type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Parse returns the header of the compact JWT raw, with no check of its
// signature: the issuer of the token picks the keys, and Verify then checks
// the signature with them. It refuses a token that does not have the form of
// a signed JWT.
func Parse(raw string) (Header, error) {
	p, err := split(raw)
	if err != nil {
		return Header{Alg: "", Kid: ""}, err
	}

	return Header{Alg: p.header.Alg, Kid: p.header.Kid}, nil
}

// Header is the part of a JWT header that a caller reads before the check
// of the signature.
type Header struct {
	Alg string
	Kid string
}

// UnverifiedIssuer returns the iss claim of raw with no check of its
// signature. The exchange uses it only to find the rules of that issuer;
// Verify then checks the token with the keys of a rule.
func UnverifiedIssuer(raw string) (string, error) {
	p, err := split(raw)
	if err != nil {
		return "", err
	}

	claims, err := decodeObject(p.payload)
	if err != nil {
		return "", fmt.Errorf("%w: payload: %w", ErrToken, err)
	}

	iss, ok := stringClaim(claims, "iss")
	if !ok {
		return "", fmt.Errorf("%w: the iss claim is missing or not a string", ErrToken)
	}

	return iss, nil
}

// Verify checks the signature of the compact JWT raw with the key of keys
// that the token names, and returns the token. It refuses, with ErrToken:
//   - a token longer than MaxTokenBytes, or with a part that is not
//     unpadded base64url, or not three parts,
//   - a header that is not a JSON object, that has a member twice, or a
//     member other than alg, kid, typ, x5t and x5t#S256,
//   - a typ other than JWT,
//   - an alg outside Algorithms, among them none and each HMAC algorithm,
//   - a kid that no key of keys has, or no kid when keys holds more than one
//     key,
//   - a key whose type or curve does not fit the alg, or whose own alg is
//     another alg,
//   - a signature that does not verify,
//   - a payload that is not a JSON object, or that has a member twice.
func Verify(raw string, keys KeySet) (Token, error) {
	p, err := split(raw)
	if err != nil {
		return Token{}, err
	}

	h := p.header

	if !slices.Contains(Algorithms(), h.Alg) {
		return Token{}, fmt.Errorf("%w: the algorithm %q is not allowed", ErrToken, h.Alg)
	}

	key, ok := keys.find(h.Kid)
	if !ok {
		return Token{}, fmt.Errorf("%w: no key of the issuer has the kid %q", ErrToken, h.Kid)
	}

	if key.Alg != "" && key.Alg != h.Alg {
		return Token{}, fmt.Errorf("%w: the key %q is for %q, not %q", ErrToken, key.ID, key.Alg, h.Alg)
	}

	if !algFits(h.Alg, key.Public) {
		return Token{}, fmt.Errorf("%w: the key %q does not fit the algorithm %q", ErrToken, key.ID, h.Alg)
	}

	if sigErr := verifySignature(h.Alg, key.Public, p.input, p.signature); sigErr != nil {
		return Token{}, fmt.Errorf("%w: %w", ErrToken, sigErr)
	}

	claims, err := decodeObject(p.payload)
	if err != nil {
		return Token{}, fmt.Errorf("%w: payload: %w", ErrToken, err)
	}

	return Token{Claims: claims, Alg: h.Alg, Kid: h.Kid}, nil
}

// parts holds the decoded parts of a JWT: the checked header, the signing
// input, the signature and the payload.
type parts struct {
	header    header
	input     []byte
	signature []byte
	payload   []byte
}

// jwtParts is the number of parts of a signed JWT.
const jwtParts = 3

// split decodes the three parts of raw and checks the header.
func split(raw string) (parts, error) {
	var none parts

	if len(raw) > MaxTokenBytes {
		return none, fmt.Errorf("%w: longer than %d bytes", ErrToken, MaxTokenBytes)
	}

	pieces := strings.Split(raw, ".")
	if len(pieces) != jwtParts || pieces[0] == "" || pieces[1] == "" || pieces[2] == "" {
		return none, fmt.Errorf("%w: not three parts of a signed JWT", ErrToken)
	}

	enc := base64.RawURLEncoding.Strict()

	headerJSON, err := enc.DecodeString(pieces[0])
	if err != nil {
		return none, fmt.Errorf("%w: the header is not unpadded base64url", ErrToken)
	}

	payload, err := enc.DecodeString(pieces[1])
	if err != nil {
		return none, fmt.Errorf("%w: the payload is not unpadded base64url", ErrToken)
	}

	if !utf8.Valid(payload) {
		return none, fmt.Errorf("%w: the payload is not UTF-8", ErrToken)
	}

	signature, err := enc.DecodeString(pieces[2])
	if err != nil {
		return none, fmt.Errorf("%w: the signature is not unpadded base64url", ErrToken)
	}

	h, err := decodeHeader(headerJSON)
	if err != nil {
		return none, err
	}

	return parts{header: h, input: []byte(pieces[0] + "." + pieces[1]), signature: signature, payload: payload}, nil
}

// decodeHeader decodes and checks a JWT header.
func decodeHeader(raw []byte) (header, error) {
	var none header

	members, err := decodeObject(raw)
	if err != nil {
		return none, fmt.Errorf("%w: header: %w", ErrToken, err)
	}

	for name := range members {
		if !slices.Contains(headerMembers(), name) {
			return none, fmt.Errorf("%w: the header member %q is not allowed", ErrToken, name)
		}
	}

	var h header
	if err := json.Unmarshal(raw, &h); err != nil {
		return none, fmt.Errorf("%w: the header members alg, kid and typ are strings", ErrToken)
	}

	if _, ok := members["typ"]; ok && !strings.EqualFold(h.Typ, "JWT") {
		return none, fmt.Errorf("%w: typ %q: a token of a workload has typ JWT", ErrToken, h.Typ)
	}

	return h, nil
}

// Errors of the decode of a part of a JWT. Each comes wrapped in ErrToken.
var (
	errNotObject    = errors.New("not a JSON object")
	errTrailingData = errors.New("data after the JSON object")
	errTwice        = errors.New("a member comes twice")
	errSignature    = errors.New("the signature does not verify")
)

// decodeObject decodes one JSON object and refuses a member that comes twice,
// trailing data, and a value that is not an object.
func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))

	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errNotObject
	}

	out := map[string]json.RawMessage{}

	for dec.More() {
		keyTok, keyErr := dec.Token()
		if keyErr != nil {
			return nil, errNotObject
		}

		name, ok := keyTok.(string)
		if !ok {
			return nil, errNotObject
		}

		if _, seen := out[name]; seen {
			return nil, fmt.Errorf("%w: %q", errTwice, name)
		}

		var value json.RawMessage
		if decErr := dec.Decode(&value); decErr != nil {
			return nil, errNotObject
		}

		out[name] = value
	}

	if tok, err = dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errNotObject
	}

	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailingData
	}

	return out, nil
}

// verifySignature checks sig over input with pub for alg. The caller checked
// that pub fits alg.
func verifySignature(alg string, pub crypto.PublicKey, input, sig []byte) error {
	errBad := errSignature

	switch k := pub.(type) {
	case *rsa.PublicKey:
		h, digest := digestOf(alg, input)

		var err error
		if strings.HasPrefix(alg, "PS") {
			err = rsa.VerifyPSS(k, h, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h})
		} else {
			err = rsa.VerifyPKCS1v15(k, h, digest, sig)
		}

		if err != nil {
			return errBad
		}

		return nil
	case *ecdsa.PublicKey:
		_, digest := digestOf(alg, input)

		size := (k.Curve.Params().BitSize + bitsPerByte - 1) / bitsPerByte
		if len(sig) != 2*size {
			return errBad
		}

		r := new(big.Int).SetBytes(sig[:size])
		s := new(big.Int).SetBytes(sig[size:])

		if !ecdsa.Verify(k, digest, r, s) {
			return errBad
		}

		return nil
	case ed25519.PublicKey:
		if !ed25519.Verify(k, input, sig) {
			return errBad
		}

		return nil
	default:
		return errBad
	}
}

// bitsPerByte is the number of bits of a byte, for the size of a coordinate
// of an EC signature.
const bitsPerByte = 8

// digestOf returns the hash of alg and the digest of input.
func digestOf(alg string, input []byte) (crypto.Hash, []byte) {
	var (
		h  hash.Hash
		id crypto.Hash
	)

	switch alg[len(alg)-3:] {
	case "384":
		h, id = sha512.New384(), crypto.SHA384
	case "512":
		h, id = sha512.New(), crypto.SHA512
	default:
		h, id = sha256.New(), crypto.SHA256
	}

	h.Write(input)

	return id, h.Sum(nil)
}

// CheckTime checks the time claims of t at now, with ClockSkew in both
// directions. It returns ErrToken when exp is missing, when now is at or
// after exp plus the skew, when exp is more than MaxTokenLifetime after now,
// when nbf is after now plus the skew, or when iat is after now plus the
// skew. A time claim that is not a number from 0 to 1e11 fails too.
func (t *Token) CheckTime(now time.Time) error {
	exp, ok, err := t.dateClaim("exp")
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("%w: the exp claim is missing", ErrToken)
	}

	if !now.Before(exp.Add(ClockSkew)) {
		return fmt.Errorf("%w: the token expired at %s", ErrToken, exp.UTC().Format(time.RFC3339))
	}

	if exp.Sub(now) > MaxTokenLifetime {
		return fmt.Errorf("%w: the token expires more than %s after now", ErrToken, MaxTokenLifetime)
	}

	for _, name := range []string{"nbf", "iat"} {
		at, found, dateErr := t.dateClaim(name)
		if dateErr != nil {
			return dateErr
		}

		if found && at.After(now.Add(ClockSkew)) {
			return fmt.Errorf("%w: the %s claim is in the future", ErrToken, name)
		}
	}

	return nil
}

// CheckIssuer returns ErrToken when the iss claim of t is not exactly issuer.
func (t *Token) CheckIssuer(issuer string) error {
	iss, ok := stringClaim(t.Claims, "iss")
	if !ok || iss != issuer {
		return fmt.Errorf("%w: the iss claim is not the issuer of the rule", ErrToken)
	}

	return nil
}

// CheckAudience returns ErrToken when the aud claim of t is neither exactly
// audience nor a list of strings that holds exactly audience. The comparison
// is exact: no prefix, no case folding, no trailing slash.
func (t *Token) CheckAudience(audience string) error {
	raw, ok := t.Claims["aud"]
	if !ok {
		return fmt.Errorf("%w: the aud claim is missing", ErrToken)
	}

	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if one == audience {
			return nil
		}

		return fmt.Errorf("%w: the aud claim is not the audience of the rule", ErrToken)
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return fmt.Errorf("%w: the aud claim is not a string or a list of strings", ErrToken)
	}

	if slices.Contains(many, audience) {
		return nil
	}

	return fmt.Errorf("%w: the aud claim does not hold the audience of the rule", ErrToken)
}

// Subject returns the sub claim of t, or the empty string when it is missing
// or not a string.
func (t *Token) Subject() string {
	sub, _ := stringClaim(t.Claims, SubjectClaim)

	return sub
}

// ID returns the jti claim of t, or the empty string when it is missing or
// not a string.
func (t *Token) ID() string {
	jti, _ := stringClaim(t.Claims, "jti")

	return jti
}

// dateClaim returns the time of the NumericDate claim name, and false when
// t has no such claim.
func (t *Token) dateClaim(name string) (time.Time, bool, error) {
	raw, ok := t.Claims[name]
	if !ok {
		return time.Time{}, false, nil
	}

	var n json.Number

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := dec.Decode(&n); err != nil {
		return time.Time{}, false, fmt.Errorf("%w: the %s claim is not a number", ErrToken, name)
	}

	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || f < 0 || f > maxNumericDate {
		return time.Time{}, false, fmt.Errorf("%w: the %s claim is not a time", ErrToken, name)
	}

	sec, frac := math.Modf(f)

	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(), true, nil
}

// stringClaim returns the claim name of claims when it is a JSON string.
func stringClaim(claims map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := claims[name]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}

	return s, true
}

// Matches reports whether the claims of t meet each condition of r: for each
// condition, the claim is a JSON string and exactly one of its values. A
// claim that is missing, or that is a number, a boolean, a list, an object or
// null, meets no condition.
func (r *Rule) Matches(t *Token) bool {
	if len(r.Conditions) == 0 {
		// Defense in depth: Validate refuses a rule with no condition, and the
		// store refuses to keep one. This check stays so that a rule that
		// skipped both never matches every token of its issuer.
		return false
	}

	hasSubject := false

	for _, c := range r.Conditions {
		v, ok := stringClaim(t.Claims, c.Claim)
		if !ok || !slices.Contains(c.Values, v) {
			return false
		}

		if c.Claim == SubjectClaim {
			hasSubject = true
		}
	}

	// Defense in depth: Validate refuses a rule with no condition on the
	// subject. This check stays so that a rule that skipped it never matches
	// the tokens of every customer of a shared issuer.
	return hasSubject
}

// find returns the key with the ID kid. An empty kid finds the one key of a
// set that holds exactly one key, and nothing else.
func (s KeySet) find(kid string) (Key, bool) {
	if kid == "" {
		if len(s.keys) == 1 {
			return s.keys[0], true
		}

		return Key{Public: nil, ID: "", Alg: ""}, false
	}

	for _, k := range s.keys {
		if k.ID == kid {
			return k, true
		}
	}

	return Key{Public: nil, ID: "", Alg: ""}, false
}
