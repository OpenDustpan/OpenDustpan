package headerauth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net/url"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Times of the key cache.
const (
	// KeyMaxAge is the longest time that the method uses the key set of one
	// read of a JWKS URL. A key of the AWS ALB does not expire: its key ID
	// always names the same key.
	KeyMaxAge = 15 * time.Minute
	// KeyRetryAfter is the shortest time between two reads of a JWKS URL,
	// and the window of the budget of new key IDs of the AWS ALB.
	KeyRetryAfter = 30 * time.Second
	// maxALBKeys is the most keys of the AWS ALB that the cache holds.
	maxALBKeys = 32
	// newALBKeyReads is the most reads of new key IDs of the AWS ALB in
	// KeyRetryAfter. A read of a key ID that a request chose does not block
	// the first read of a new key of the load balancer.
	newALBKeyReads = 3
	// es256Size is the size of one coordinate of an ES256 signature.
	es256Size = 32
	// jwtParts is the number of parts of a signed JWT.
	jwtParts = 3
)

// presetAlgorithms is the algorithm allowlist of the presets.
func presetAlgorithms() []string {
	return []string{"ES256"}
}

// albHeaderMembers are the members that a JWT header of the AWS ALB can
// hold.
func albHeaderMembers() []string {
	return []string{"alg", "kid", "signer", "iss", "client", "exp", "typ"}
}

// algorithms returns the algorithm allowlist of the method: ES256 for a
// preset; ES256, ES384, RS256 and stronger, and EdDSA for a custom issuer.
// None of them is none or an HMAC algorithm.
func (m *Method) algorithms() []string {
	if m.preset != PresetCustom {
		return presetAlgorithms()
	}

	return trustrule.Algorithms()
}

// answerError wraps err in ErrMethodAnswer.
func answerError(err error) error {
	return fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
}

// verifyJWKS checks a JWT of Google IAP or of a custom issuer against the
// keys of its JWKS URL.
func (m *Method) verifyJWKS(ctx context.Context, raw string) (trustrule.Token, error) {
	h, err := trustrule.Parse(raw)
	if err != nil {
		return trustrule.Token{}, answerError(err)
	}

	if !slices.Contains(m.algorithms(), h.Alg) {
		return trustrule.Token{}, fmt.Errorf("%w: the algorithm %q is not allowed", session.ErrMethodAnswer, h.Alg)
	}

	keys, err := m.keys.jwks(ctx, h.Kid)
	if err != nil {
		return trustrule.Token{}, err
	}

	tok, err := trustrule.Verify(raw, keys)
	if err != nil {
		return trustrule.Token{}, answerError(err)
	}

	// Defense in depth: the check of the header above refuses the
	// algorithm first, and trustrule.Verify refuses none and HMAC. This
	// check stays so that a token that passed the narrower list of a
	// preset by another path still fails.
	if !slices.Contains(m.algorithms(), tok.Alg) {
		return trustrule.Token{}, fmt.Errorf("%w: the algorithm %q is not allowed", session.ErrMethodAnswer, tok.Alg)
	}

	// Google IAP and a custom issuer send iat. The AWS ALB does not.
	if _, ok := tok.Claims["iat"]; !ok {
		return trustrule.Token{}, fmt.Errorf("%w: the iat claim is missing", session.ErrMethodAnswer)
	}

	if err := m.checkClaims(&tok); err != nil {
		return trustrule.Token{}, err
	}

	return tok, nil
}

// verifyALB checks a JWT of the AWS ALB: ES256, a key ID in the allowlist,
// the signer of the configured load balancer, and the key of its key URL.
func (m *Method) verifyALB(ctx context.Context, raw string) (trustrule.Token, error) {
	var none trustrule.Token

	pieces := strings.Split(raw, ".")
	if len(pieces) != jwtParts {
		return none, fmt.Errorf("%w: not three parts of a signed JWT", session.ErrMethodAnswer)
	}

	decoded := make([][]byte, len(pieces))

	for i, p := range pieces {
		b, err := decodeALBPart(p)
		if err != nil {
			return none, err
		}

		decoded[i] = b
	}

	kid, header, err := m.checkALBHeader(decoded[0])
	if err != nil {
		return none, err
	}

	pub, err := m.keys.albKeyOf(ctx, kid)
	if err != nil {
		return none, err
	}

	if !verifyES256(pub, []byte(pieces[0]+"."+pieces[1]), decoded[2]) {
		return none, fmt.Errorf("%w: the signature does not verify", session.ErrMethodAnswer)
	}

	claims, err := decodeObject(decoded[1])
	if err != nil {
		return none, answerError(fmt.Errorf("payload: %w", err))
	}

	if err := m.checkALBPayload(claims, header); err != nil {
		return none, err
	}

	return trustrule.Token{Claims: claims, Alg: "ES256", Kid: kid}, nil
}

// checkALBPayload checks the iss and the time claims of the payload of a
// JWT of the AWS ALB. The AWS ALB puts exp and iss in the header, and the
// header check requires them. A payload iss must be the issuer too. A
// payload exp, iat or nbf must pass the time check too; a payload with no
// exp is checked with the exp of the header.
func (m *Method) checkALBPayload(claims, header map[string]json.RawMessage) error {
	if _, ok := claims["iss"]; ok {
		payload := trustrule.Token{Claims: claims, Alg: "", Kid: ""}
		if err := payload.CheckIssuer(m.issuer); err != nil {
			return answerError(err)
		}
	}

	times := maps.Clone(claims)
	if _, ok := times["exp"]; !ok {
		times["exp"] = header["exp"]
	}

	payload := trustrule.Token{Claims: times, Alg: "", Kid: ""}
	if err := payload.CheckTime(m.now()); err != nil {
		return answerError(err)
	}

	return nil
}

// checkALBHeader checks the decoded JWT header of the AWS ALB and returns
// its key ID and its members. It checks the members that the AWS ALB puts in
// the header: alg, kid, signer, the client ID when the method has one, iss
// and exp. It returns ErrMethodInput for a key ID outside the allowlist, and
// ErrMethodAnswer for each other failed check.
func (m *Method) checkALBHeader(raw []byte) (string, map[string]json.RawMessage, error) {
	header, err := decodeObject(raw)
	if err != nil {
		return "", nil, answerError(fmt.Errorf("header: %w", err))
	}

	for name := range header {
		if !slices.Contains(albHeaderMembers(), name) {
			return "", nil, fmt.Errorf("%w: the header member %q is not allowed", session.ErrMethodAnswer, name)
		}
	}

	if alg, _ := stringMember(header, "alg"); !slices.Contains(presetAlgorithms(), alg) {
		return "", nil, fmt.Errorf("%w: the algorithm %q is not allowed", session.ErrMethodAnswer, alg)
	}

	kid, _ := stringMember(header, "kid")
	if !albKeyIDPattern.MatchString(kid) {
		return "", nil, fmt.Errorf("%w: the key ID is outside the allowlist", session.ErrMethodInput)
	}

	if signer, _ := stringMember(header, "signer"); signer != m.signerARN {
		return "", nil, fmt.Errorf("%w: the signer is not the configured load balancer", session.ErrMethodAnswer)
	}

	if client, _ := stringMember(header, "client"); m.albClient != "" && client != m.albClient {
		return "", nil, fmt.Errorf("%w: the client is not the configured client ID", session.ErrMethodAnswer)
	}

	// The header holds iss and exp. CheckTime requires exp.
	tok := trustrule.Token{Claims: header, Alg: "", Kid: ""}
	if err := tok.CheckIssuer(m.issuer); err != nil {
		return "", nil, answerError(fmt.Errorf("header: %w", err))
	}

	if err := tok.CheckTime(m.now()); err != nil {
		return "", nil, answerError(fmt.Errorf("header: %w", err))
	}

	return kid, header, nil
}

// decodeALBPart decodes one part of a JWT of the AWS ALB: base64url, with
// or without padding. The AWS ALB pads its parts.
func decodeALBPart(p string) ([]byte, error) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(p, "="), "=")
	if trimmed == "" || strings.Contains(trimmed, "=") {
		return nil, fmt.Errorf("%w: a part of the token is empty or not base64url", session.ErrMethodAnswer)
	}

	b, err := base64.RawURLEncoding.Strict().DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: a part of the token is not base64url", session.ErrMethodAnswer)
	}

	return b, nil
}

// verifyES256 checks the ES256 signature sig over input with pub.
func verifyES256(pub *ecdsa.PublicKey, input, sig []byte) bool {
	if pub == nil || pub.Curve != elliptic.P256() || len(sig) != 2*es256Size {
		return false
	}

	digest := sha256.Sum256(input)
	r := new(big.Int).SetBytes(sig[:es256Size])
	s := new(big.Int).SetBytes(sig[es256Size:])

	return ecdsa.Verify(pub, digest[:], r, s)
}

// checkClaims checks the issuer, the audience and the times of tok.
func (m *Method) checkClaims(tok *trustrule.Token) error {
	if err := tok.CheckIssuer(m.issuer); err != nil {
		return answerError(err)
	}

	if m.preset != PresetAWSALB {
		if err := tok.CheckAudience(m.audience); err != nil {
			return answerError(err)
		}
	}

	if err := tok.CheckTime(m.now()); err != nil {
		return answerError(err)
	}

	return nil
}

// assertionOf returns the Assertion of a checked token: the subject from
// sub, the email, and the groups from the groups claim.
func (m *Method) assertionOf(tok *trustrule.Token) (session.Assertion, error) {
	var none session.Assertion

	// The subject is used byte for byte. It is never case-folded or
	// normalized, so two subjects that only look the same stay two persons.
	sub := tok.Subject()
	if sub == "" || len(sub) > MaxSubject || !session.IsSafeText(sub) {
		return none, fmt.Errorf("%w: the sub claim is missing or not a valid subject", session.ErrMethodAnswer)
	}

	a := headerAssertion(m.providerID, sub)

	if email, ok := stringMember(tok.Claims, "email"); ok {
		if len(email) > session.MaxAssertionName || !session.IsSafeText(email) {
			return none, fmt.Errorf("%w: the email claim is not valid", session.ErrMethodAnswer)
		}

		a.Email = email
	}

	if m.groupsClaim == "" {
		return a, nil
	}

	groups, err := groupsOf(tok.Claims, m.groupsClaim)
	if err != nil {
		return none, err
	}

	a.Groups, a.GroupsPresent = groups, true

	return a, nil
}

// groupsOf returns the groups of the claim name. It returns
// ErrGroupsMissing when the claim is missing, and ErrMethodAnswer for a
// claim that is not a list of valid group names or that holds more than
// the limit.
func groupsOf(claims map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := claims[name]
	if !ok {
		return nil, fmt.Errorf("%w: the groups claim is missing", session.ErrGroupsMissing)
	}

	var groups []string
	if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
		return nil, fmt.Errorf("%w: the groups claim is not a list of strings", session.ErrMethodAnswer)
	}

	if len(groups) > session.MaxAssertionGroups {
		return nil, fmt.Errorf("%w: more than %d groups", session.ErrMethodAnswer, session.MaxAssertionGroups)
	}

	for _, g := range groups {
		if g == "" || len(g) > session.MaxAssertionGroup || !session.IsSafeText(g) {
			return nil, fmt.Errorf("%w: a group of the groups claim is not valid", session.ErrMethodAnswer)
		}
	}

	return groups, nil
}

// Errors of the decode of a JSON object.
var (
	errNotObject = errors.New("not a JSON object")
	errTwice     = errors.New("a member comes twice")
	errTrailing  = errors.New("data after the JSON object")
)

// decodeObject decodes one JSON object and refuses a member that comes
// twice, trailing data, and a value that is not an object.
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
		return nil, errTrailing
	}

	return out, nil
}

// stringMember returns the member name of members when it is a JSON
// string.
func stringMember(members map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := members[name]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}

	return s, true
}

// ALBKeyURL returns the key URL of the key ID kid of the AWS ALB under the
// base URL base. It returns ErrMethodInput for a key ID outside the
// allowlist ^[A-Za-z0-9-]{1,128}$, so that no key ID can change the path or
// the host of the URL.
func ALBKeyURL(base, kid string) (string, error) {
	// Defense in depth: verifyALB checks the key ID against the allowlist
	// before it asks for the key. This check stays so that a new caller
	// cannot put a path into the key URL.
	if !albKeyIDPattern.MatchString(kid) {
		return "", fmt.Errorf("%w: the key ID is outside the allowlist", session.ErrMethodInput)
	}

	// The escape changes no character of the allowlist. It stays so that
	// the value is encoded where it is used.
	return base + url.PathEscape(kid), nil
}
