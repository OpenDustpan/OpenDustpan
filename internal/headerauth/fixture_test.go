package headerauth_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/headerauth"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// Values of the test proxy.
const (
	providerID   = "corp-proxy"
	sharedSecret = "a-shared-secret-of-the-test-proxy-0123456789"
	keyID        = "key-1"
	albARN       = "arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/test/50dc6c495c0c9188"
	albIssuer    = "https://idp.example.com"
	albClient    = "app-client"
	customIss    = "https://issuer.example.com"
	audience     = "/projects/123/global/backendServices/456"
	groupsClaim  = "groups"
	groupsHeader = "X-Auth-Groups"
	keyTimeout   = 10 * time.Second
	// hangTimeout is the key timeout of the method of the hang row. Only
	// the method of the hang row uses it, so that no other row depends on
	// a fast key read.
	hangTimeout = 300 * time.Millisecond
	// keyServerHits is the room of the hits channel of a key server.
	keyServerHits = 64
	rsaBits       = 2048
	es256Coord    = 32
)

// proxyPeer is the address of the trusted test proxy.
func proxyPeer() netip.Addr { return netip.MustParseAddr("10.0.0.5") }

// untrustedPeer is an address outside each proxy list.
func untrustedPeer() netip.Addr { return netip.MustParseAddr("192.0.2.7") }

// clientProxyPeer is an address of a proxy that only forwards the client
// address.
func clientProxyPeer() netip.Addr { return netip.MustParseAddr("10.1.0.9") }

// fixedNow is the clock of the tests.
func fixedNow() time.Time {
	return time.Unix(1_800_000_000, 0).UTC()
}

// proxies is the proxy list of the test provider.
func proxies() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("2001:db8::1/128")}
}

// alg names a signing algorithm of the tests.
type alg string

// Algorithms of the test issuers.
const (
	es256 alg = "ES256"
	rs256 alg = "RS256"
	edDSA alg = "EdDSA"
)

// signer signs a JWT with one key.
type signer struct {
	public crypto.PublicKey
	sign   func(input []byte) ([]byte, error)
	alg    alg
}

// newSigner returns a signer with a new key of the algorithm a.
func newSigner(t *testing.T, a alg) signer {
	t.Helper()

	switch a {
	case rs256:
		k, err := rsa.GenerateKey(rand.Reader, rsaBits)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}

		return rsaSigner(k)
	case edDSA:
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}

		return signer{
			public: k.Public(),
			alg:    a,
			sign:   func(in []byte) ([]byte, error) { return ed25519.Sign(k, in), nil },
		}
	case es256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}

		return signer{public: k.Public(), alg: a, sign: func(in []byte) ([]byte, error) { return signES256(k, in) }}
	default:
		t.Fatalf("unknown algorithm %q", a)

		return signer{public: nil, sign: nil, alg: ""}
	}
}

// rsaSigner returns the RS256 signer of k.
func rsaSigner(k *rsa.PrivateKey) signer {
	return signer{public: k.Public(), alg: rs256, sign: func(in []byte) ([]byte, error) {
		digest := sha256.Sum256(in)

		return rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest[:])
	}}
}

// signES256 returns the ES256 signature of input: r and s, 32 bytes each.
func signES256(k *ecdsa.PrivateKey, input []byte) ([]byte, error) {
	digest := sha256.Sum256(input)

	r, s, err := ecdsa.Sign(rand.Reader, k, digest[:])
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	out := make([]byte, 2*es256Coord)
	r.FillBytes(out[:es256Coord])
	s.FillBytes(out[es256Coord:])

	return out, nil
}

// encodeToken returns the compact JWT of h and c signed by s, with the
// encoding enc of its parts. The AWS ALB pads its parts.
func encodeToken(t *testing.T, s signer, h, c map[string]any, enc *base64.Encoding) string {
	t.Helper()

	hb, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	cb, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	input := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)

	sig, err := s.sign([]byte(input))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	return input + "." + enc.EncodeToString(sig)
}

// jwkOf returns the public JWK of key with the key ID kid.
func jwkOf(t *testing.T, key crypto.PublicKey, kid string, a alg) map[string]any {
	t.Helper()

	b64 := base64.RawURLEncoding.EncodeToString

	switch k := key.(type) {
	case *ecdsa.PublicKey:
		raw, err := k.Bytes()
		if err != nil {
			t.Fatalf("key bytes: %v", err)
		}

		// raw is 0x04, then x, then y.
		return map[string]any{
			"kty": "EC", "crv": "P-256", "x": b64(raw[1 : 1+es256Coord]), "y": b64(raw[1+es256Coord:]),
			"kid": kid, "alg": string(a), "use": "sig",
		}
	case *rsa.PublicKey:
		return map[string]any{
			"kty": "RSA", "n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes()), "kid": kid,
			"alg": string(a), "use": "sig",
		}
	case ed25519.PublicKey:
		return map[string]any{
			"kty": "OKP",
			"crv": tls.Ed25519.String(),
			"x":   b64(k),
			"kid": kid,
			"alg": string(a),
			"use": "sig",
		}
	default:
		t.Fatalf("unknown key type %T", key)

		return nil
	}
}

// encodedKey is a public key in PEM and in DER.
type encodedKey struct {
	pem []byte
	der []byte
}

// encodeKey returns the PKIX PEM and DER of key.
func encodeKey(t *testing.T, key crypto.PublicKey) encodedKey {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return encodedKey{pem: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Headers: nil, Bytes: der}), der: der}
}

// keyServer is the key endpoint of a test issuer.
type keyServer struct {
	server *httptest.Server
	body   map[string][]byte
	// redirect maps a path to the URL of a redirect answer.
	redirect map[string]string
	release  chan struct{}
	// hits gets the path of each request, when it has room.
	hits  chan string
	paths []string
	mu    sync.Mutex
	hang  atomic.Bool
}

// newKeyServer starts a TLS key endpoint that answers each path of body.
func newKeyServer(t *testing.T, body map[string][]byte) *keyServer {
	t.Helper()

	ks := &keyServer{
		server: nil, body: body, redirect: map[string]string{}, release: make(chan struct{}),
		hits: make(chan string, keyServerHits), paths: nil, mu: sync.Mutex{}, hang: atomic.Bool{},
	}
	ks.server = httptest.NewTLSServer(http.HandlerFunc(ks.serve))
	t.Cleanup(ks.server.Close)
	t.Cleanup(func() { close(ks.release) })

	return ks
}

func (ks *keyServer) serve(w http.ResponseWriter, r *http.Request) {
	ks.mu.Lock()
	ks.paths = append(ks.paths, r.URL.Path)
	to, redirect := ks.redirect[r.URL.Path]
	ks.mu.Unlock()

	select {
	case ks.hits <- r.URL.Path:
	default:
	}

	if redirect {
		http.Redirect(w, r, to, http.StatusFound)

		return
	}

	if ks.hang.Load() {
		select {
		case <-r.Context().Done():
		case <-ks.release:
		}

		return
	}

	b, ok := ks.body[r.URL.Path]
	if !ok {
		http.NotFound(w, r)

		return
	}

	if _, err := w.Write(b); err != nil {
		return
	}
}

// requests returns the paths of the requests.
func (ks *keyServer) requests() []string {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	return append([]string(nil), ks.paths...)
}

// client returns a client with the egress rules, which trusts the
// certificate of the key server.
func (ks *keyServer) client(t *testing.T, rules egress.Rules) *http.Client {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(ks.server.Certificate())

	c := egresshttp.ClientWith(
		rules,
		egress.NoRedirect,
		egress.HTTPSOnly,
		egresshttp.Options{RootCAs: pool, Resolver: nil},
	)
	t.Cleanup(c.CloseIdleConnections)

	return c
}

// loopback returns the egress rules of the tests: loopback allowed, so that
// the local key server passes.
func loopback() egress.Rules {
	return egress.Rules{Allow: nil, LoopbackForTests: true}
}

// newMethod returns a method of cfg and fails the test on an error.
func newMethod(t *testing.T, cfg headerauth.Config) *headerauth.Method {
	t.Helper()

	m, err := headerauth.New(cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return m
}

// facts returns request facts with the headers h from the proxy.
func facts(h map[string][]string) session.RequestFacts {
	return session.RequestFacts{
		ReceivedAt: fixedNow(), Headers: h, ProviderID: providerID, ClientChain: nil,
		Peer: proxyPeer(), Client: netip.MustParseAddr("198.51.100.20"),
	}
}

// plainConfig returns the configuration of the plain test provider with
// the groups header groups, or with no groups when it is empty.
func plainConfig(off *atomic.Bool, groups string) headerauth.Config {
	return headerauth.Config{
		HTTPClient: nil, Now: fixedNow, ProviderID: providerID, Mode: headerauth.ModePlain,
		TrustedProxies: proxies(), UserHeader: "X-Auth-User", GroupsHeader: groups, SecretHeader: "x-proxy-secret",
		Secret: session.NewPassword(sharedSecret), KeyTimeout: 0,
		Enabled: func(context.Context) (bool, error) { return !off.Load(), nil },
		JWT: headerauth.JWTConfig{
			Preset: headerauth.PresetCustom, Issuer: "", Audience: "", KeyURL: "", Region: "", SignerARN: "",
			ClientID: "", GroupsClaim: "",
		},
	}
}

// plainFixture returns the fixture of the plain mode with the groups header
// groups, or with no groups when it is empty.
func plainFixture(groups string) func(t *testing.T) sessiontest.RequestFixture {
	return func(t *testing.T) sessiontest.RequestFixture {
		t.Helper()

		off := &atomic.Bool{}
		m := newMethod(t, plainConfig(off, groups))

		return sessiontest.RequestFixture{
			Method: m,
			Facts: func(_ *testing.T, p sessiontest.TestPerson) session.RequestFacts {
				h := map[string][]string{"X-Auth-User": {p.Subject}, "X-Proxy-Secret": {sharedSecret}}
				if groups != "" && p.GroupsPresent {
					h[groups] = []string{strings.Join(p.Groups, ",")}
				}

				return facts(h)
			},
			Disable: func(*testing.T) { off.Store(true) }, Stop: nil, Hang: nil, Refused: nil, Now: fixedNow,
			JWT: nil, ProviderID: providerID, UserHeader: "X-Auth-User", SecretHeader: "X-Proxy-Secret",
			UntrustedPeer: untrustedPeer(), ClientProxyPeer: clientProxyPeer(), Groups: groups != "",
		}
	}
}

// jwtSetup describes one test issuer of the jwt mode.
type jwtSetup struct {
	preset headerauth.Preset
	claim  string
	alg    alg
}

// encodingOf returns the encoding of the parts of a token of the preset.
func encodingOf(p headerauth.Preset) *base64.Encoding {
	if p == headerauth.PresetAWSALB {
		return base64.URLEncoding
	}

	return base64.RawURLEncoding
}

// issuerKeys are the key of a test issuer and another key of the same
// kind.
type issuerKeys struct {
	good, other signer
}

// jwtFixture returns the fixture of a test issuer of the jwt mode over
// the keys k.
func jwtFixture(s jwtSetup, k issuerKeys) func(t *testing.T) sessiontest.RequestFixture {
	return func(t *testing.T) sessiontest.RequestFixture {
		t.Helper()

		enc := encodeKey(t, k.good.public)

		jwkJSON, err := json.Marshal(jwkOf(t, k.good.public, keyID, s.alg))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		jwks := []byte(`{"keys":[` + string(jwkJSON) + `]}`)
		ks := newKeyServer(t, map[string][]byte{"/jwks": jwks, "/alb/" + keyID: enc.pem})

		off := &atomic.Bool{}
		cfg := jwtConfig(s, ks, off)
		cfg.HTTPClient = ks.client(t, loopback())
		m := newMethod(t, cfg)

		shape := jwtShapes(s)
		alb := s.preset == headerauth.PresetAWSALB

		sign := func(sg signer) func(t *testing.T, h, c map[string]any) string {
			return func(t *testing.T, h, c map[string]any) string {
				t.Helper()

				return encodeToken(t, sg, h, c, encodingOf(s.preset))
			}
		}

		f := sessiontest.RequestFixture{
			Method: m,
			Facts: func(t *testing.T, p sessiontest.TestPerson) session.RequestFacts {
				t.Helper()

				raw := encodeToken(t, k.good, shape.header(), shape.claims(p), encodingOf(s.preset))

				return facts(map[string][]string{shape.user: {raw}})
			},
			Disable: func(*testing.T) { off.Store(true) },
			Stop:    func(*testing.T) { ks.server.Close() },
			Hang: func(t *testing.T) session.RequestMethod {
				t.Helper()

				ks.hang.Store(true)

				hangCfg := jwtConfig(s, ks, off)
				hangCfg.HTTPClient = ks.client(t, loopback())
				hangCfg.KeyTimeout = hangTimeout

				return newMethod(t, hangCfg)
			},
			Refused: func(t *testing.T) session.RequestMethod {
				t.Helper()

				refusedCfg := jwtConfig(s, ks, off)
				refusedCfg.HTTPClient = ks.client(t, egress.Rules{Allow: nil, LoopbackForTests: false})

				return newMethod(t, refusedCfg)
			},
			Now: fixedNow,
			JWT: &sessiontest.RequestJWT{
				Header: shape.header, Claims: shape.claims, Sign: sign(k.good), SignOther: sign(k.other),
				KeyRequests: ks.requests, PublicKeys: [][]byte{enc.pem, enc.der, jwkJSON, jwks},
				SignerMember: "", Audience: !alb, KeyIDInURL: alb, HeaderClaims: alb,
			},
			ProviderID: providerID, UserHeader: shape.user, SecretHeader: "",
			UntrustedPeer: untrustedPeer(), ClientProxyPeer: clientProxyPeer(), Groups: s.claim != "",
		}

		if alb {
			f.JWT.SignerMember = "signer"
		}

		return f
	}
}

// jwtConfig returns the configuration of the test issuer s over ks.
func jwtConfig(s jwtSetup, ks *keyServer, off *atomic.Bool) headerauth.Config {
	cfg := headerauth.Config{
		HTTPClient: nil, Now: fixedNow, ProviderID: providerID, Mode: headerauth.ModeJWT,
		TrustedProxies: proxies(), UserHeader: "", GroupsHeader: "", SecretHeader: "",
		Secret: session.NewPassword(""), KeyTimeout: keyTimeout,
		Enabled: func(context.Context) (bool, error) { return !off.Load(), nil },
		JWT: headerauth.JWTConfig{
			Preset: s.preset, Issuer: "", Audience: audience, KeyURL: ks.server.URL + "/jwks", Region: "",
			SignerARN: "", ClientID: "", GroupsClaim: s.claim,
		},
	}

	// Google IAP keeps the values of its preset.
	if s.preset == headerauth.PresetAWSALB {
		cfg.JWT.Issuer, cfg.JWT.Audience, cfg.JWT.SignerARN, cfg.JWT.ClientID = albIssuer, "", albARN, albClient
		cfg.JWT.KeyURL = ks.server.URL + "/alb/"
	}

	if s.preset == headerauth.PresetCustom {
		cfg.JWT.Issuer, cfg.UserHeader = customIss, "X-Auth-Token"
	}

	return cfg
}

// tokenShape is the valid header, the valid claims and the user header of
// a test issuer.
type tokenShape struct {
	header func() map[string]any
	claims func(p sessiontest.TestPerson) map[string]any
	user   string
}

// jwtShapes returns the token shape of the test issuer s.
func jwtShapes(s jwtSetup) tokenShape {
	issuer, user := headerauth.GoogleIAPIssuer, headerauth.GoogleIAPHeader

	if s.preset == headerauth.PresetAWSALB {
		issuer, user = albIssuer, headerauth.AWSALBHeader
	}

	if s.preset == headerauth.PresetCustom {
		issuer, user = customIss, "X-Auth-Token"
	}

	header := func() map[string]any {
		h := map[string]any{"alg": string(s.alg), "kid": keyID, "typ": "JWT"}
		if s.preset == headerauth.PresetAWSALB {
			h["signer"], h["iss"], h["client"] = albARN, albIssuer, albClient
			h["exp"] = fixedNow().Add(time.Minute).Unix()
		}

		return h
	}

	claims := func(p sessiontest.TestPerson) map[string]any {
		// The AWS ALB sends the claims of the user-info endpoint of the
		// identity provider, and puts iss and exp in the header.
		c := map[string]any{"sub": p.Subject, "email": p.Email, "name": p.Name}
		if s.preset != headerauth.PresetAWSALB {
			c["iss"], c["aud"] = issuer, audience
			c["exp"], c["iat"] = fixedNow().Add(5*time.Minute).Unix(), fixedNow().Add(-10*time.Second).Unix()
		}

		if s.claim != "" && p.GroupsPresent {
			g := p.Groups
			if g == nil {
				g = []string{}
			}

			c[s.claim] = g
		}

		return c
	}

	return tokenShape{header: header, claims: claims, user: user}
}

// newIssuerKeys returns the keys of a test issuer of the algorithm a.
func newIssuerKeys(t *testing.T, a alg) issuerKeys {
	t.Helper()

	return issuerKeys{good: newSigner(t, a), other: newSigner(t, a)}
}

func TestRequestMethodSuite(t *testing.T) {
	t.Parallel()

	// The keys are made once for each issuer: an RSA key costs time.
	ec, rs, ed := newIssuerKeys(t, es256), newIssuerKeys(t, rs256), newIssuerKeys(t, edDSA)

	for name, factory := range map[string]func(t *testing.T) sessiontest.RequestFixture{
		"Plain":             plainFixture(groupsHeader),
		"PlainNoGroups":     plainFixture(""),
		"GoogleIAP":         jwtFixture(jwtSetup{preset: headerauth.PresetGoogleIAP, alg: es256, claim: groupsClaim}, ec),
		"AWSALB":            jwtFixture(jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""}, ec),
		"AWSALBGroups":      jwtFixture(jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: groupsClaim}, ec),
		"CustomRS256":       jwtFixture(jwtSetup{preset: headerauth.PresetCustom, alg: rs256, claim: groupsClaim}, rs),
		"CustomEdDSA":       jwtFixture(jwtSetup{preset: headerauth.PresetCustom, alg: edDSA, claim: ""}, ed),
		"CustomES256Groups": jwtFixture(jwtSetup{preset: headerauth.PresetCustom, alg: es256, claim: groupsClaim}, ec),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sessiontest.RunRequestMethod(t, factory)
		})
	}
}
