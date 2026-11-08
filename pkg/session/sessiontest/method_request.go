package sessiontest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"hash"
	"maps"
	"math/big"
	"net/netip"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// RequestFixture is a request method with a test proxy and, in the jwt
// mode, a test issuer.
type RequestFixture struct {
	// Method is the method under test.
	Method session.RequestMethod
	// Facts returns the facts of a request that the trusted test proxy
	// sends for the person p, with each proof of the proxy. A person with
	// GroupsPresent false gets no groups header and no groups claim.
	Facts func(t *testing.T, p TestPerson) session.RequestFacts
	// Disable turns the provider of the method off.
	Disable func(t *testing.T)
	// Stop makes the key endpoint of the test issuer stop answering. It is
	// nil for a method with no key endpoint.
	Stop func(t *testing.T)
	// Hang makes the key endpoint of the test issuer answer later than the
	// timeout of the method that it returns. The method is of the same
	// provider and has a short key timeout. It is nil for a method with no
	// key endpoint.
	Hang func(t *testing.T) session.RequestMethod
	// Refused returns a method of the same provider whose key URL resolves
	// to an address that the egress rules refuse. It is nil for a method
	// with no key endpoint.
	Refused func(t *testing.T) session.RequestMethod
	// Now is the clock of the method.
	Now func() time.Time
	// JWT holds the test issuer of the jwt mode, or is nil for a method
	// that reads a plain header.
	JWT *RequestJWT
	// ProviderID names the provider of the method.
	ProviderID string
	// UserHeader is the canonical name of the header of the person.
	UserHeader string
	// SecretHeader is the canonical name of the header of the shared
	// secret, or empty for a method with no shared secret.
	SecretHeader string
	// UntrustedPeer is an address outside the proxy list of the provider.
	UntrustedPeer netip.Addr
	// ClientProxyPeer is an address in the proxy list that the server uses
	// to read the client address, and not in the proxy list of the
	// provider.
	ClientProxyPeer netip.Addr
	// Groups is true for a method that reads groups.
	Groups bool
}

// RequestJWT is the test issuer of a request method in the jwt mode.
type RequestJWT struct {
	// Header returns a valid JWT header.
	Header func() map[string]any
	// Claims returns valid claims for the person p, at the clock of the
	// method.
	Claims func(p TestPerson) map[string]any
	// Sign returns the token of the header and the claims, signed with the
	// key of the test issuer. It changes no member.
	Sign func(t *testing.T, header, claims map[string]any) string
	// SignOther is Sign with another key of the same kind and the same key
	// ID.
	SignOther func(t *testing.T, header, claims map[string]any) string
	// KeyRequests returns the path of each request to the key endpoint.
	KeyRequests func() []string
	// PublicKeys are the forms of the public key of the issuer that a
	// confused method could use as an HMAC secret.
	PublicKeys [][]byte
	// SignerMember is the header member that names the signer, or empty
	// for an issuer with no signer.
	SignerMember string
	// Audience is true for an issuer that sends an audience.
	Audience bool
	// KeyIDInURL is true for an issuer whose key ID goes into the key URL.
	KeyIDInURL bool
	// HeaderClaims is true for an issuer that puts iss and exp in the JWT
	// header, as the AWS ALB does. The cases then change them there.
	HeaderClaims bool
}

// requestCase is one case of RunRequestMethod.
type requestCase struct {
	run  func(t *testing.T, f RequestFixture)
	when func(f RequestFixture) bool
	name string
}

// Conditions of the request cases.
func reqHasGroups(f RequestFixture) bool { return f.Groups }
func reqNoGroups(f RequestFixture) bool  { return !f.Groups }
func reqPlain(f RequestFixture) bool     { return f.JWT == nil }
func reqJWT(f RequestFixture) bool       { return f.JWT != nil }
func reqAudience(f RequestFixture) bool  { return f.JWT != nil && f.JWT.Audience }
func reqKeyInURL(f RequestFixture) bool  { return f.JWT != nil && f.JWT.KeyIDInURL }
func reqSigner(f RequestFixture) bool    { return f.JWT != nil && f.JWT.SignerMember != "" }

// RunRequestMethod runs the conformance suite of a session.RequestMethod.
// The factory returns a fixture over a fresh test proxy and test issuer.
// Each case that sends hostile input tests one layer alone: the peer, the
// boundary of the headers, the signature and the claims, the time limits,
// the second proof of the proxy, and the fail-closed answers.
func RunRequestMethod(t *testing.T, factory func(t *testing.T) RequestFixture) {
	t.Helper()

	probe := factory(t)

	cases := requestCases()
	cases = append(cases, requestBoundaryCases()...)
	cases = append(cases, requestJWTCases()...)
	cases = append(cases, requestProofCases()...)

	for _, c := range cases {
		if c.when != nil && !c.when(probe) {
			continue
		}

		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, factory(t))
		})
	}
}

// requestCases are the cases of a valid request and of the answers that
// are not a sign-in.
func requestCases() []requestCase {
	return []requestCase{
		{name: "ARequestOfTheTrustedProxyReturnsTheAssertion", when: nil, run: rqReturnsTheAssertion},
		{name: "HeadersNamesEachHeaderThatTheMethodReads", when: nil, run: rqHeadersNamesEachHeader},
		{name: "TheAssertionHasAuthTimeKnownFalse", when: nil, run: rqAuthTimeKnownFalse},
		{name: "TheAssertionHoldsNoFactor", when: nil, run: rqNoFactor},
		{name: "TheAssertionHasTheKindOfTheMethod", when: nil, run: rqKind},
		{name: "GroupsComeFromTheGroupsHeaderOrClaim", when: reqHasGroups, run: rqGroups},
		{name: "AnEmptyGroupsHeaderIsPresentAndEmpty", when: reqHasGroups, run: rqEmptyGroups},
		{name: "AMethodWithNoGroupsReturnsGroupsPresentFalse", when: reqNoGroups, run: rqNoGroups},
		{name: "ARequestWithNoHeaderIsErrNoAssertion", when: nil, run: rqNoHeader},
		{name: "ADisabledProviderReturnsErrProviderDisabled", when: nil, run: rqDisabled},
		{name: "GroupsOnAndNoGroupsHeaderIsErrGroupsMissing", when: reqHasGroups, run: rqGroupsMissing},
		{name: "EachErrorReturnsTheZeroAssertion", when: nil, run: rqEachErrorZero},
	}
}

// requestBoundaryCases are the cases of the peer and of the form of the
// headers.
func requestBoundaryCases() []requestCase {
	return []requestCase{
		{name: "APeerOutsideTheProxyListOfTheProviderIsErrUntrustedSource", when: nil, run: rqUntrustedPeer},
		{name: "APeerInTheClientAddressProxiesOnlyIsErrUntrustedSource", when: nil, run: rqClientProxyPeer},
		{name: "ASubjectOutsideTheAllowlistIsErrMethodInput", when: reqPlain, run: rqSubjectAllowlist},
		{name: "AHeaderWithControlCharactersIsErrMethodInput", when: nil, run: rqControlCharacters},
		{name: "TwoValuesOfTheUserHeaderAreErrMethodInput", when: nil, run: rqTwoUsers},
		{name: "MoreGroupsThanTheLimitAreErrMethodAnswer", when: reqHasGroups, run: rqTooManyGroups},
		{name: "AHeaderThatTheMethodDoesNotNameIsNotRead", when: nil, run: rqUnnamedHeader},
	}
}

// requestJWTCases are the cases of the signed header.
func requestJWTCases() []requestCase {
	return []requestCase{
		{name: "AJWTWithAnotherIssuerIsErrMethodAnswer", when: reqJWT, run: rqOtherIssuer},
		{name: "AJWTWithAnotherAudienceIsErrMethodAnswer", when: reqAudience, run: rqOtherAudience},
		{name: "AJWTSignedWithAnotherKeyIsErrMethodAnswer", when: reqJWT, run: rqOtherKey},
		{name: "AJWTWithTheAlgorithmNoneIsErrMethodAnswer", when: reqJWT, run: rqAlgNone},
		{name: "AJWTWithAnAlgorithmOutsideTheAllowlistIsErrMethodAnswer", when: reqJWT, run: rqAlgHMAC},
		{name: "AKeyIDOutsideTheAllowlistIsErrMethodInputBeforeTheKeyURL", when: reqKeyInURL, run: rqKeyIDAllowlist},
		{name: "AnALBSignerThatIsNotTheConfiguredARNIsErrMethodAnswer", when: reqSigner, run: rqOtherSigner},
		{name: "TheKeyURLGoesThroughTheEgressRules", when: reqJWT, run: rqKeyURLEgress},
		{name: "AJWTOutsideTheClockSkewIsErrMethodAnswer", when: reqJWT, run: rqFutureIssuedAt},
		{name: "AnExpiredJWTIsErrMethodAnswer", when: reqJWT, run: rqExpired},
		{name: "AJWTWithNoSubjectIsErrMethodAnswer", when: reqJWT, run: rqNoSubject},
		{name: "AKeyEndpointThatDoesNotAnswerInTimeIsErrMethodUnavailable", when: reqJWT, run: rqKeyHang},
		{name: "AStoppedKeyEndpointIsErrMethodUnavailable", when: reqJWT, run: rqKeyStopped},
	}
}

// requestProofCases are the cases of the second proof of the plain mode.
func requestProofCases() []requestCase {
	return []requestCase{
		{name: "APlainHeaderFromATrustedPeerWithNoSecondProofIsErrUntrustedSource", when: reqPlain, run: rqNoSecret},
		{name: "AWrongSharedSecretIsErrUntrustedSource", when: reqPlain, run: rqWrongSecret},
		{name: "AClientCertificateOfAnotherBundleIsErrUntrustedSource", when: reqPlain, run: rqClientCertificate},
	}
}

// carol is the person of the request cases. Her subject passes the
// subject allowlist of each mode.
func carol() TestPerson {
	return TestPerson{
		AuthTime: time.Time{}, Subject: "carol-0001", UserName: "carol", Name: "Carol Example",
		Email: "carol@example.com", Groups: []string{"ops", "dev"}, GroupsPresent: true, Disabled: false,
	}
}

// requestOf returns the Assertion of the facts and fails the test on an
// error or an Assertion that the sign-in service refuses.
func requestOf(t *testing.T, f RequestFixture, facts session.RequestFacts) session.Assertion {
	t.Helper()

	a, err := f.Method.FromRequest(t.Context(), facts)
	if err != nil {
		t.Fatalf("FromRequest() = %v", err)
	}

	checkValid(t, a, f.Now())

	return a
}

// requestRefused runs the facts and checks that the method returns want and the
// zero Assertion.
func requestRefused(t *testing.T, m session.RequestMethod, facts session.RequestFacts, want error) {
	t.Helper()

	a, err := m.FromRequest(t.Context(), facts)
	checkRefused(t, a, err, want)
}

// tokenFacts returns the facts of p with a token whose header and claims
// mutate changed, signed with the key of the test issuer.
func tokenFacts(t *testing.T, f RequestFixture, p TestPerson, mutate func(h, c map[string]any)) session.RequestFacts {
	t.Helper()

	h, c := f.JWT.Header(), f.JWT.Claims(p)
	mutate(h, c)

	facts := f.Facts(t, p)
	facts.Headers[f.UserHeader] = []string{f.JWT.Sign(t, h, c)}

	return facts
}

// withToken returns the facts of p with the raw token.
func withToken(t *testing.T, f RequestFixture, p TestPerson, raw string) session.RequestFacts {
	t.Helper()

	facts := f.Facts(t, p)
	facts.Headers[f.UserHeader] = []string{raw}

	return facts
}

func rqReturnsTheAssertion(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	a := requestOf(t, f, f.Facts(t, p))

	if a.Subject != p.Subject || a.ProviderID != f.ProviderID || a.PrincipalID != "" {
		t.Fatalf("Assertion = %+v, want the subject %q of the provider %q", a, p.Subject, f.ProviderID)
	}
}

func rqHeadersNamesEachHeader(t *testing.T, f RequestFixture) {
	t.Helper()

	names := f.Method.Headers()

	for _, h := range names {
		if h != textproto.CanonicalMIMEHeaderKey(h) {
			t.Fatalf("Headers() holds %q, which is not canonical", h)
		}
	}

	for name := range f.Facts(t, carol()).Headers {
		if !slices.Contains(names, name) {
			t.Fatalf("the request of the proxy carries %q, which Headers() %v does not name", name, names)
		}
	}

	for _, want := range []string{f.UserHeader, f.SecretHeader} {
		if want != "" && !slices.Contains(names, want) {
			t.Fatalf("Headers() = %v, want it to hold %q", names, want)
		}
	}
}

func rqAuthTimeKnownFalse(t *testing.T, f RequestFixture) {
	t.Helper()

	a := requestOf(t, f, f.Facts(t, carol()))

	if a.AuthTimeKnown || !a.AuthTime.IsZero() {
		t.Fatalf("AuthTimeKnown = %v, AuthTime = %v, want false and zero", a.AuthTimeKnown, a.AuthTime)
	}
}

func rqNoFactor(t *testing.T, f RequestFixture) {
	t.Helper()

	if a := requestOf(t, f, f.Facts(t, carol())); len(a.Factors) != 0 {
		t.Fatalf("Factors = %v, want none", a.Factors)
	}
}

func rqKind(t *testing.T, f RequestFixture) {
	t.Helper()

	a := requestOf(t, f, f.Facts(t, carol()))

	if a.Method != f.Method.Kind() || a.Method != session.MethodHeader {
		t.Fatalf("Method = %q, Kind() = %q, want %q", a.Method, f.Method.Kind(), session.MethodHeader)
	}
}

func rqGroups(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	checkGroups(t, requestOf(t, f, f.Facts(t, p)), p)
}

func rqEmptyGroups(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	p.Groups = nil

	a := requestOf(t, f, f.Facts(t, p))
	if !a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want an empty list that is present", a.Groups, a.GroupsPresent)
	}
}

func rqNoGroups(t *testing.T, f RequestFixture) {
	t.Helper()

	if a := requestOf(t, f, f.Facts(t, carol())); a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want none", a.Groups, a.GroupsPresent)
	}
}

func rqNoHeader(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	facts.Headers = map[string][]string{}
	requestRefused(t, f.Method, facts, session.ErrNoAssertion)
}

func rqDisabled(t *testing.T, f RequestFixture) {
	t.Helper()

	f.Disable(t)
	requestRefused(t, f.Method, f.Facts(t, carol()), session.ErrProviderDisabled)
}

func rqGroupsMissing(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	p.Groups, p.GroupsPresent = nil, false
	requestRefused(t, f.Method, f.Facts(t, p), session.ErrGroupsMissing)
}

func rqEachErrorZero(t *testing.T, f RequestFixture) {
	t.Helper()

	edits := []func(facts *session.RequestFacts){
		func(facts *session.RequestFacts) { facts.Peer = f.UntrustedPeer },
		func(facts *session.RequestFacts) { facts.Headers = map[string][]string{} },
		func(facts *session.RequestFacts) {
			facts.Headers[f.UserHeader] = append(facts.Headers[f.UserHeader], facts.Headers[f.UserHeader]...)
		},
		func(facts *session.RequestFacts) { facts.ProviderID = "another" },
	}

	for _, edit := range edits {
		facts := f.Facts(t, carol())
		edit(&facts)

		a, err := f.Method.FromRequest(t.Context(), facts)
		if err == nil || !zeroAssertion(a) {
			t.Fatalf("FromRequest() = %+v, %v, want an error and the zero Assertion", a, err)
		}
	}
}

func rqUntrustedPeer(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	trusted := facts.Peer
	facts.Peer = f.UntrustedPeer
	// The client address names the trusted proxy: the method must read the
	// peer of the connection, never the forwarded client address.
	facts.Client = trusted
	requestRefused(t, f.Method, facts, session.ErrUntrustedSource)

	facts.Peer = netip.Addr{}
	requestRefused(t, f.Method, facts, session.ErrUntrustedSource)
}

func rqClientProxyPeer(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	facts.Peer = f.ClientProxyPeer
	requestRefused(t, f.Method, facts, session.ErrUntrustedSource)
}

func rqSubjectAllowlist(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, s := range []string{
		"carol smith", "carol/../admin", "carol,admin", "carol;admin", "<carol>", "carol\"", "carol*",
		"carol\u00e9", strings.Repeat("a", session.MaxAssertionSubject+1),
	} {
		facts := f.Facts(t, carol())
		facts.Headers[f.UserHeader] = []string{s}
		requestRefused(t, f.Method, facts, session.ErrMethodInput)
	}
}

func rqControlCharacters(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, bad := range []string{"\r\nX-Admin: 1", "\x00", "\x7f", "\t"} {
		facts := f.Facts(t, carol())

		users := facts.Headers[f.UserHeader]
		if len(users) != 1 {
			t.Fatalf("the request of the proxy carries %d values of %q, want one", len(users), f.UserHeader)
		}

		facts.Headers[f.UserHeader] = []string{users[0] + bad}
		requestRefused(t, f.Method, facts, session.ErrMethodInput)
	}
}

func rqTwoUsers(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	other := f.Facts(t, TestPerson{
		AuthTime: time.Time{}, Subject: "mallory", UserName: "mallory", Name: "", Email: "",
		Groups: []string{"admins"}, GroupsPresent: true, Disabled: false,
	})
	facts.Headers[f.UserHeader] = append(facts.Headers[f.UserHeader], other.Headers[f.UserHeader]...)
	requestRefused(t, f.Method, facts, session.ErrMethodInput)
}

func rqTooManyGroups(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	// Short names, so that a signed header of the groups stays under the
	// size limit of a token: the case tests the count of the groups.
	p.Groups = make([]string, session.MaxAssertionGroups+1)
	for i := range p.Groups {
		p.Groups[i] = strconv.FormatInt(int64(i), 36)
	}

	requestRefused(t, f.Method, f.Facts(t, p), session.ErrMethodAnswer)
}

func rqUnnamedHeader(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, name := range []string{"X-Forwarded-User", "X-Remote-User", strings.ToLower(f.UserHeader)} {
		facts := f.Facts(t, carol())
		value := facts.Headers[f.UserHeader]
		delete(facts.Headers, f.UserHeader)
		facts.Headers[name] = value
		requestRefused(t, f.Method, facts, session.ErrNoAssertion)
	}
}

func rqOtherIssuer(t *testing.T, f RequestFixture) {
	t.Helper()

	other := "https://issuer.example.net"
	facts := tokenFacts(t, f, carol(), func(_, c map[string]any) { c["iss"] = other })
	requestRefused(t, f.Method, facts, session.ErrMethodAnswer)

	if !f.JWT.HeaderClaims {
		return
	}

	for _, iss := range []any{other, nil, 1} {
		facts = tokenFacts(t, f, carol(), func(h, _ map[string]any) { setOrDelete(h, "iss", iss) })
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
	}
}

// setOrDelete sets the member name of m to v, or deletes it when v is nil.
func setOrDelete(m map[string]any, name string, v any) {
	if v == nil {
		delete(m, name)

		return
	}

	m[name] = v
}

func rqOtherAudience(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, aud := range []any{"another-audience", []string{"another-audience"}, nil} {
		facts := tokenFacts(t, f, carol(), func(_, c map[string]any) {
			if aud == nil {
				delete(c, "aud")
			} else {
				c["aud"] = aud
			}
		})
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
	}
}

func rqOtherKey(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	raw := f.JWT.SignOther(t, f.JWT.Header(), f.JWT.Claims(p))
	requestRefused(t, f.Method, withToken(t, f, p, raw), session.ErrMethodAnswer)
}

// unsigned returns the parts of a token of h and c: base64url of each
// JSON, joined with a dot, with no signature.
func unsigned(t *testing.T, h, c map[string]any) string {
	t.Helper()

	hb, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	cb, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
}

func rqAlgNone(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()

	for _, alg := range []string{"none", "None", "NONE", ""} {
		h := f.JWT.Header()
		h["alg"] = alg
		input := unsigned(t, h, f.JWT.Claims(p))

		for _, raw := range []string{input + ".", input, input + ".c2lnbmF0dXJl"} {
			requestRefused(t, f.Method, withToken(t, f, p, raw), session.ErrMethodAnswer)
		}
	}
}

func rqAlgHMAC(t *testing.T, f RequestFixture) {
	t.Helper()

	p := carol()
	algs := map[string]func() hash.Hash{"HS256": sha256.New, "HS384": sha512.New384, "HS512": sha512.New}

	for _, alg := range slices.Sorted(maps.Keys(algs)) {
		// Key confusion: the public key of the issuer as the HMAC secret.
		for _, key := range f.JWT.PublicKeys {
			h := f.JWT.Header()
			h["alg"] = alg
			input := unsigned(t, h, f.JWT.Claims(p))

			mac := hmac.New(algs[alg], key)
			mac.Write([]byte(input))
			raw := input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

			requestRefused(t, f.Method, withToken(t, f, p, raw), session.ErrMethodAnswer)
		}
	}
}

func rqKeyIDAllowlist(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, kid := range []string{
		"../keys", "..%2fkeys", "a/b", "a.b", "a?b", "a#b", "a b", "a%00", "",
		"@evil.example.com", strings.Repeat("a", 129),
	} {
		facts := tokenFacts(t, f, carol(), func(h, _ map[string]any) { h["kid"] = kid })
		requestRefused(t, f.Method, facts, session.ErrMethodInput)
	}

	if got := f.JWT.KeyRequests(); len(got) != 0 {
		t.Fatalf("the key endpoint got %v, want no request", got)
	}
}

func rqOtherSigner(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, signer := range []any{"arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/other/1", nil, 1} {
		facts := tokenFacts(t, f, carol(), func(h, _ map[string]any) {
			if signer == nil {
				delete(h, f.JWT.SignerMember)
			} else {
				h[f.JWT.SignerMember] = signer
			}
		})
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
	}
}

func rqKeyURLEgress(t *testing.T, f RequestFixture) {
	t.Helper()

	requestRefused(t, f.Refused(t), f.Facts(t, carol()), session.ErrMethodUnavailable)
}

func rqFutureIssuedAt(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, d := range []time.Duration{2 * time.Minute, 24 * time.Hour} {
		facts := tokenFacts(t, f, carol(), func(_, c map[string]any) { c["iat"] = f.Now().Add(d).Unix() })
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
	}
}

func rqExpired(t *testing.T, f RequestFixture) {
	t.Helper()

	pastSec := f.Now().Add(-2 * time.Minute).Unix()
	values := []any{pastSec, "tomorrow", f.Now().Add(48 * time.Hour).Unix()}

	if f.JWT.HeaderClaims {
		// The exp of the header is required; a payload exp is checked too.
		facts := tokenFacts(t, f, carol(), func(_, c map[string]any) { c["exp"] = pastSec })
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)

		for _, exp := range append(values, nil) {
			facts = tokenFacts(t, f, carol(), func(h, _ map[string]any) { setOrDelete(h, "exp", exp) })
			requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
		}

		return
	}

	for _, exp := range append(values, nil) {
		facts := tokenFacts(t, f, carol(), func(_, c map[string]any) { setOrDelete(c, "exp", exp) })
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
	}
}

func rqNoSubject(t *testing.T, f RequestFixture) {
	t.Helper()

	for _, sub := range []any{nil, "", 42, "carol\x00", "carol\u202e", "car\u200bol", "carol\u2028admin", "\ufeffcarol"} {
		facts := tokenFacts(t, f, carol(), func(_, c map[string]any) {
			if sub == nil {
				delete(c, "sub")
			} else {
				c["sub"] = sub
			}
		})
		requestRefused(t, f.Method, facts, session.ErrMethodAnswer)
	}
}

func rqKeyHang(t *testing.T, f RequestFixture) {
	t.Helper()

	m := f.Hang(t)
	requestRefused(t, m, f.Facts(t, carol()), session.ErrMethodUnavailable)
}

func rqKeyStopped(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	f.Stop(t)
	requestRefused(t, f.Method, facts, session.ErrMethodUnavailable)
}

func rqNoSecret(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	delete(facts.Headers, f.SecretHeader)
	requestRefused(t, f.Method, facts, session.ErrUntrustedSource)
}

func rqWrongSecret(t *testing.T, f RequestFixture) {
	t.Helper()

	secrets := f.Facts(t, carol()).Headers[f.SecretHeader]
	if len(secrets) != 1 || secrets[0] == "" {
		t.Fatalf("the request of the proxy carries %d values of %q, want one secret", len(secrets), f.SecretHeader)
	}

	good := secrets[0]

	for _, v := range [][]string{
		{good[:len(good)-1]},
		{good + "x"},
		{strings.ToUpper(good)},
		{""},
		{good, good},
		{strings.Repeat("x", len(good))},
	} {
		facts := f.Facts(t, carol())
		facts.Headers[f.SecretHeader] = v
		requestRefused(t, f.Method, facts, session.ErrUntrustedSource)
	}
}

func rqClientCertificate(t *testing.T, f RequestFixture) {
	t.Helper()

	facts := f.Facts(t, carol())
	delete(facts.Headers, f.SecretHeader)
	facts.ClientChain = []*x509.Certificate{selfSigned(t)}
	requestRefused(t, f.Method, facts, session.ErrUntrustedSource)
}

// selfSigned returns a new self-signed certificate of no bundle.
func selfSigned(t *testing.T) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// The template sets only the fields that a client certificate needs.
	tmpl := new(x509.Certificate)
	tmpl.SerialNumber = big.NewInt(1)
	tmpl.Subject.CommonName = "proxy.example.com"
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	return cert
}
