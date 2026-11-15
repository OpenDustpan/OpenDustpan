package headerauth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/headerauth"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// errStoreDown is the error of a provider state that cannot be read.
var errStoreDown = errors.New("store down")

// Layer 1, the peer: Strip removes each header of the method from a request
// whose TCP peer is not in the proxy list, also when a forwarded header
// names a trusted proxy.
func TestStripRemovesForgedHeadersOfAnUntrustedPeer(t *testing.T) {
	t.Parallel()

	m := newMethod(t, plainConfig(&atomic.Bool{}, groupsHeader))

	tests := []struct {
		header     http.Header
		name       string
		remoteAddr string
		refused    bool
	}{
		{
			name: "forged user from an untrusted peer", remoteAddr: "192.0.2.7:4000", refused: true,
			header: http.Header{"X-Auth-User": {"admin"}, "X-Forwarded-For": {"10.0.0.5"}},
		},
		{
			name: "forged groups and secret", remoteAddr: "192.0.2.7:4000", refused: true,
			header: http.Header{"X-Auth-Groups": {"admins"}, "X-Proxy-Secret": {"guess"}},
		},
		{
			name: "a name that is not canonical", remoteAddr: "192.0.2.7:4000", refused: true,
			header: http.Header{"x-auth-user": {"admin"}, "X-AUTH-GROUPS": {"admins"}},
		},
		{
			name: "an underscore for a hyphen", remoteAddr: "192.0.2.7:4000", refused: true,
			header: http.Header{"X_Auth_User": {"admin"}, "x_proxy-secret": {"guess"}},
		},
		{
			name: "a peer in the client address proxies only", remoteAddr: "10.1.0.9:4000", refused: true,
			header: http.Header{"X-Auth-User": {"admin"}},
		},
		{
			name: "an IPv4-mapped trusted peer keeps its headers", remoteAddr: "[::ffff:10.0.0.5]:4000",
			refused: false, header: http.Header{"X-Auth-User": {"carol"}},
		},
		{
			name: "a trusted peer keeps its headers", remoteAddr: "10.0.0.5:4000", refused: false,
			header: http.Header{"X-Auth-User": {"carol"}, "X-Auth-Groups": {"ops"}},
		},
		{
			name: "no header of the method", remoteAddr: "192.0.2.7:4000", refused: false,
			header: http.Header{"Accept": {"*/*"}},
		},
		{
			name: "a remote address with no address", remoteAddr: "pipe", refused: true,
			header: http.Header{"X-Auth-User": {"admin"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.RemoteAddr = tc.remoteAddr
			r.Header = tc.header.Clone()

			if got := m.Strip(r, headerauth.PeerOf(r)); got != tc.refused {
				t.Fatalf("Strip() = %v, want %v", got, tc.refused)
			}

			if tc.refused {
				checkStripped(t, r.Header)
			} else if len(r.Header) != len(tc.header) {
				t.Fatalf("Strip removed headers of a trusted peer: %v", r.Header)
			}
		})
	}
}

// checkStripped fails when a header of the method stays in got after a
// refusal.
func checkStripped(t *testing.T, got http.Header) {
	t.Helper()

	for name := range got {
		switch http.CanonicalHeaderKey(strings.ReplaceAll(name, "_", "-")) {
		case "X-Auth-User", groupsHeader, "X-Proxy-Secret":
			t.Fatalf("the header %q stays after Strip", name)
		default:
		}
	}
}

// The wiring of the HTTP adapter: it reads the facts of the request, runs
// the sign-in, and removes the headers that no handler may read before the
// next handler runs. The next handler reads no secret of the proxy, after
// a sign-in and after each refusal.
func TestTheSecretHeaderNeverReachesTheHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		header     http.Header
		name       string
		remoteAddr string
		wantErr    error
		off        bool
	}{
		{
			name: "a sign-in of the proxy", remoteAddr: "10.0.0.5:4000", wantErr: nil, off: false,
			header: http.Header{
				"X-Auth-User": {"carol"}, "X-Auth-Groups": {"ops,dev"}, "X-Proxy-Secret": {sharedSecret},
			},
		},
		{
			name: "a wrong secret", remoteAddr: "10.0.0.5:4000", wantErr: session.ErrUntrustedSource, off: false,
			header: http.Header{"X-Auth-User": {"carol"}, "X-Proxy-Secret": {"not-the-secret"}},
		},
		{
			name: "no secret", remoteAddr: "10.0.0.5:4000", wantErr: session.ErrUntrustedSource, off: false,
			header: http.Header{"X-Auth-User": {"carol"}},
		},
		{
			name: "an untrusted peer with a forged secret", remoteAddr: "192.0.2.7:4000",
			wantErr: session.ErrUntrustedSource, off: false,
			header: http.Header{"X-Auth-User": {"admin"}, "X-Proxy-Secret": {sharedSecret}},
		},
		{
			name: "a disabled provider", remoteAddr: "10.0.0.5:4000",
			wantErr: session.ErrProviderDisabled, off: true,
			header: http.Header{"X-Auth-User": {"carol"}, "X-Proxy-Secret": {sharedSecret}},
		},
		{
			name: "a secret in two spellings", remoteAddr: "10.0.0.5:4000", wantErr: nil, off: false,
			header: http.Header{
				"X-Auth-User": {"carol"}, "X-Auth-Groups": {"ops"},
				"X-Proxy-Secret": {sharedSecret}, "x_proxy_secret": {sharedSecret},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			off := &atomic.Bool{}
			off.Store(tc.off)
			m := newMethod(t, plainConfig(off, groupsHeader))

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.RemoteAddr = tc.remoteAddr
			r.Header = tc.header.Clone()

			// The HTTP adapter reads the facts of the request and runs the
			// sign-in.
			if _, err := m.FromRequest(t.Context(), requestFacts(r, m)); !errors.Is(err, tc.wantErr) {
				t.Fatalf("FromRequest() = %v, want %v", err, tc.wantErr)
			}

			// Then it removes the headers that no handler may read: each
			// header of the method for an untrusted peer, and the secret
			// header that the sign-in consumed.
			m.Strip(r, headerauth.PeerOf(r))
			m.Consume(r)

			var seen http.Header

			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
			next.ServeHTTP(httptest.NewRecorder(), r)

			if seen == nil {
				t.Fatal("the next handler did not run")
			}

			checkNoSecret(t, seen)

			if tc.wantErr == nil && seen.Get("X-Auth-User") != "carol" {
				t.Fatalf("the next handler reads the user header %q, want %q", seen.Get("X-Auth-User"), "carol")
			}
		})
	}
}

// Consume removes only the secret header of the plain mode. The jwt mode
// has no secret header: Consume reports false and removes nothing.
func TestConsumeRemovesOnlyTheSecretHeader(t *testing.T) {
	t.Parallel()

	jwtCfg := headerauth.Config{
		HTTPClient: http.DefaultClient, Now: fixedNow, ProviderID: providerID, Mode: headerauth.ModeJWT,
		TrustedProxies: proxies(), Enabled: nil, UserHeader: "X-Auth-Token", GroupsHeader: "", SecretHeader: "",
		Secret: session.NewPassword(""), KeyTimeout: 0,
		JWT: headerauth.JWTConfig{
			Preset: headerauth.PresetCustom, Issuer: customIss, Audience: audience,
			KeyURL: "https://issuer.example.com/jwks", Region: "", SignerARN: "", ClientID: "", GroupsClaim: "",
		},
	}

	tests := []struct {
		cfg         headerauth.Config
		header      http.Header
		want        http.Header
		name        string
		wantRemoved bool
	}{
		{
			name: "the plain mode", cfg: plainConfig(&atomic.Bool{}, groupsHeader), wantRemoved: true,
			header: http.Header{"X-Auth-User": {"carol"}, "X-Proxy-Secret": {sharedSecret}},
			want:   http.Header{"X-Auth-User": {"carol"}},
		},
		{
			name: "the jwt mode", cfg: jwtCfg, wantRemoved: false,
			header: http.Header{"X-Auth-Token": {"a.b.c"}, "X-Proxy-Secret": {sharedSecret}},
			want:   http.Header{"X-Auth-Token": {"a.b.c"}, "X-Proxy-Secret": {sharedSecret}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newMethod(t, tc.cfg)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.Header = tc.header.Clone()

			if got := m.Consume(r); got != tc.wantRemoved {
				t.Fatalf("Consume() = %v, want %v", got, tc.wantRemoved)
			}

			if len(r.Header) != len(tc.want) {
				t.Fatalf("Consume left the headers %v, want %v", r.Header, tc.want)
			}

			for name, values := range tc.want {
				if !slices.Equal(r.Header[name], values) {
					t.Fatalf("Consume left %s = %v, want %v", name, r.Header[name], values)
				}
			}
		})
	}
}

// checkNoSecret fails when a spelling of the secret header stays in got.
func checkNoSecret(t *testing.T, got http.Header) {
	t.Helper()

	for name := range got {
		if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), "X-Proxy-Secret") {
			t.Fatalf("the next handler reads the secret header %q", name)
		}
	}
}

// requestFacts returns the facts of r that the HTTP adapter builds: the
// peer, the client address, and only the headers that the method names,
// with canonical names.
func requestFacts(r *http.Request, m *headerauth.Method) session.RequestFacts {
	headers := make(map[string][]string)

	for _, h := range m.Headers() {
		if v, ok := r.Header[http.CanonicalHeaderKey(h)]; ok {
			headers[h] = slices.Clone(v)
		}
	}

	return session.RequestFacts{
		ReceivedAt: fixedNow(), Headers: headers, ProviderID: providerID, ClientChain: nil,
		Peer: headerauth.PeerOf(r), Client: netip.MustParseAddr("198.51.100.20"),
	}
}

// Layer 5, fail closed: an empty proxy list turns the provider off and
// strips the headers of every peer.
func TestAnEmptyProxyListTurnsTheProviderOff(t *testing.T) {
	t.Parallel()

	cfg := plainConfig(&atomic.Bool{}, "")
	cfg.TrustedProxies = nil
	m := newMethod(t, cfg)

	f := facts(map[string][]string{"X-Auth-User": {"carol"}, "X-Proxy-Secret": {sharedSecret}})

	if _, err := m.FromRequest(t.Context(), f); !errors.Is(err, session.ErrProviderDisabled) {
		t.Fatalf("FromRequest() = %v, want %v", err, session.ErrProviderDisabled)
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "10.0.0.5:4000"
	r.Header.Set("X-Auth-User", "carol")

	if !m.Strip(r, headerauth.PeerOf(r)) || r.Header.Get("X-Auth-User") != "" {
		t.Fatal("Strip kept the user header with an empty proxy list")
	}
}

// Layer 5, fail closed: a provider state that cannot be read refuses the
// sign-in.
func TestAnUnreadableProviderStateIsErrMethodUnavailable(t *testing.T) {
	t.Parallel()

	cfg := plainConfig(&atomic.Bool{}, "")
	cfg.Enabled = func(_ context.Context) (bool, error) { return true, errStoreDown }
	m := newMethod(t, cfg)

	f := facts(map[string][]string{"X-Auth-User": {"carol"}, "X-Proxy-Secret": {sharedSecret}})

	if _, err := m.FromRequest(t.Context(), f); !errors.Is(err, session.ErrMethodUnavailable) {
		t.Fatalf("FromRequest() = %v, want %v", err, session.ErrMethodUnavailable)
	}
}

// Layer 1, the boundary of the configuration: New refuses a configuration
// that would weaken a layer.
func TestNewRefusesAConfigurationThatIsNotValid(t *testing.T) {
	t.Parallel()

	base := func() headerauth.Config { return plainConfig(&atomic.Bool{}, groupsHeader) }
	jwtBase := func() headerauth.Config {
		return headerauth.Config{
			HTTPClient: http.DefaultClient, Now: fixedNow, ProviderID: providerID, Mode: headerauth.ModeJWT,
			TrustedProxies: proxies(), Enabled: nil, UserHeader: "", GroupsHeader: "", SecretHeader: "",
			Secret: session.NewPassword(""), KeyTimeout: 0,
			JWT: headerauth.JWTConfig{
				Preset: headerauth.PresetCustom, Issuer: customIss, Audience: audience,
				KeyURL: "https://issuer.example.com/jwks", Region: "", SignerARN: "", ClientID: "", GroupsClaim: "",
			},
		}
	}

	tests := map[string]headerauth.Config{}
	add := func(name string, base func() headerauth.Config, edit func(c *headerauth.Config)) {
		c := base()
		edit(&c)
		tests[name] = c
	}

	add("no provider ID", base, func(c *headerauth.Config) { c.ProviderID = "" })
	add("the local provider ID", base, func(c *headerauth.Config) { c.ProviderID = session.LocalProviderID })
	add("a /0 proxy range", base, func(c *headerauth.Config) {
		c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	})
	add("a proxy range with host bits", base, func(c *headerauth.Config) {
		c.TrustedProxies = []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("10.0.0.5"), 24)}
	})
	add("an unknown mode", base, func(c *headerauth.Config) { c.Mode = "trust-me" })
	add("a short secret", base, func(c *headerauth.Config) { c.Secret = session.NewPassword("short") })
	add("a secret with a control character", base, func(c *headerauth.Config) {
		c.Secret = session.NewPassword(sharedSecret + "\n")
	})
	add("no secret header", base, func(c *headerauth.Config) { c.SecretHeader = "" })
	add("the secret in the user header", base, func(c *headerauth.Config) { c.SecretHeader = "x-auth-user" })
	add("a header name with a colon", base, func(c *headerauth.Config) { c.UserHeader = "X-User:" })
	add("JWT settings in the plain mode", base, func(c *headerauth.Config) { c.JWT.Issuer = customIss })
	add("jwt with no client", jwtBase, func(c *headerauth.Config) { c.HTTPClient = nil })
	add("jwt with a groups header", jwtBase, func(c *headerauth.Config) { c.GroupsHeader = "X-Groups" })
	add("jwt with a shared secret", jwtBase, func(c *headerauth.Config) { c.SecretHeader = "X-Secret" })
	add("jwt with no audience", jwtBase, func(c *headerauth.Config) { c.JWT.Audience = "" })
	add("jwt with no issuer", jwtBase, func(c *headerauth.Config) { c.JWT.Issuer = "" })
	add("an http key URL", jwtBase, func(c *headerauth.Config) { c.JWT.KeyURL = "http://issuer.example.com/jwks" })
	add("a key URL with a user", jwtBase, func(c *headerauth.Config) {
		// The URL is built at run time, so that the file holds no literal
		// credential URL.
		u := url.URL{Scheme: "https", User: url.UserPassword("user", "pass"), Host: "issuer.example.com", Path: "/jwks"}
		c.JWT.KeyURL = u.String()
	})
	add(
		"a key URL with a query",
		jwtBase,
		func(c *headerauth.Config) { c.JWT.KeyURL = "https://issuer.example.com/?a=1" },
	)
	add("an unknown preset", jwtBase, func(c *headerauth.Config) { c.JWT.Preset = "okta" })
	add("ALB with no ARN", jwtBase, func(c *headerauth.Config) {
		c.JWT.Preset, c.JWT.Audience, c.JWT.KeyURL, c.JWT.Region = headerauth.PresetAWSALB, "", "", "us-east-1"
	})
	add("ALB with a region that is a path", jwtBase, func(c *headerauth.Config) {
		c.JWT.Preset, c.JWT.Audience, c.JWT.KeyURL = headerauth.PresetAWSALB, "", ""
		c.JWT.Region, c.JWT.SignerARN = "evil.example.com/x", albARN
	})
	add("Google IAP with a client ID", jwtBase, func(c *headerauth.Config) {
		c.JWT.Preset, c.JWT.KeyURL, c.JWT.ClientID = headerauth.PresetGoogleIAP, "", "app-client"
	})
	add("a custom issuer with a client ID", jwtBase, func(c *headerauth.Config) { c.JWT.ClientID = "app-client" })
	add("ALB with a client ID that has a control character", jwtBase, func(c *headerauth.Config) {
		c.JWT.Preset, c.JWT.Audience, c.JWT.KeyURL = headerauth.PresetAWSALB, "", ""
		c.JWT.Region, c.JWT.SignerARN, c.JWT.ClientID = "us-east-1", albARN, "app\nclient"
	})
	add("a groups claim with a space", jwtBase, func(c *headerauth.Config) { c.JWT.GroupsClaim = "my groups" })

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := headerauth.New(cfg); !errors.Is(err, headerauth.ErrConfig) {
				t.Fatalf("New() = %v, want %v", err, headerauth.ErrConfig)
			}
		})
	}
}

// Layer 2, encode or contain: ALBKeyURL refuses each key ID outside the
// allowlist, as if the check of the token had failed.
func TestALBKeyURLRefusesAKeyIDOutsideTheAllowlist(t *testing.T) {
	t.Parallel()

	const base = "https://public-keys.auth.elb.us-east-1.amazonaws.com/"

	for _, kid := range []string{
		"../x", "..", "a/b", "a\\b", "a.b", "a?b", "a#b", "%2e%2e", "a b", "a\x00", "", "@evil.example.com",
		"\u00e4", strings.Repeat("a", 129),
	} {
		if u, err := headerauth.ALBKeyURL(base, kid); !errors.Is(err, session.ErrMethodInput) || u != "" {
			t.Fatalf("ALBKeyURL(%q) = %q, %v, want %v", kid, u, err, session.ErrMethodInput)
		}
	}

	u, err := headerauth.ALBKeyURL(base, "3e2f-a1B9")
	if err != nil || u != base+"3e2f-a1B9" {
		t.Fatalf("ALBKeyURL() = %q, %v, want the key URL", u, err)
	}
}

// Layer 2: ParseALBKey accepts only one PEM block of a P-256 public key.
func TestParseALBKeyRefusesAnyOtherKey(t *testing.T) {
	t.Parallel()

	ecPEM := encodeKey(t, newSigner(t, es256).public).pem
	rsaPEM := encodeKey(t, newSigner(t, rs256).public).pem
	edPEM := encodeKey(t, newSigner(t, edDSA).public).pem

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p384PEM := encodeKey(t, p384.Public()).pem

	for name, body := range map[string][]byte{
		"an RSA key":                        rsaPEM,
		"an Ed25519 key":                    edPEM,
		"a P-384 key":                       p384PEM,
		"two blocks":                        slices.Concat(ecPEM, ecPEM),
		"a certificate type":                []byte(strings.Replace(string(ecPEM), "PUBLIC KEY", "CERTIFICATE", 2)),
		"no PEM":                            []byte(`{"keys":[]}`),
		"empty":                             nil,
		"a key after junk":                  slices.Concat([]byte("junk\n"), ecPEM),
		"a key after a malformed block":     slices.Concat([]byte("-----BEGIN CERTIFICATE-----\nAAAA\n"), ecPEM),
		"a key with an indented first line": slices.Concat([]byte("  "), ecPEM),
	} {
		if _, err := headerauth.ParseALBKey(body); !errors.Is(err, session.ErrMethodUnavailable) {
			t.Fatalf("%s: ParseALBKey() = %v, want %v", name, err, session.ErrMethodUnavailable)
		}
	}

	for name, body := range map[string][]byte{
		"a P-256 key":                   ecPEM,
		"a P-256 key after empty lines": slices.Concat([]byte("\n\n"), ecPEM),
	} {
		if _, err := headerauth.ParseALBKey(body); err != nil {
			t.Fatalf("%s: ParseALBKey() = %v, want no error", name, err)
		}
	}
}

// Layer 2: a preset accepts ES256 only, also when its key set holds a key
// of another algorithm and the token verifies with that key.
func TestAPresetRefusesAnAlgorithmThatItsKeySetHolds(t *testing.T) {
	t.Parallel()

	ec, rs := newSigner(t, es256), newSigner(t, rs256)
	ecJWK, rsaJWK := jwkOf(t, ec.public, keyID, es256), jwkOf(t, rs.public, "rsa-1", rs256)

	set, err := json.Marshal(map[string]any{"keys": []any{ecJWK, rsaJWK}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	ks := newKeyServer(t, map[string][]byte{"/jwks": set})
	setup := jwtSetup{preset: headerauth.PresetGoogleIAP, alg: es256, claim: ""}
	cfg := jwtConfig(setup, ks, &atomic.Bool{})
	cfg.HTTPClient = ks.client(t, loopback())
	m := newMethod(t, cfg)

	raw := encodeToken(t, rs, map[string]any{"alg": "RS256", "kid": "rsa-1", "typ": "JWT"},
		jwtShapes(setup).claims(alice()), base64.RawURLEncoding)

	_, err = m.FromRequest(t.Context(), facts(map[string][]string{headerauth.GoogleIAPHeader: {raw}}))
	if !errors.Is(err, session.ErrMethodAnswer) {
		t.Fatalf("FromRequest() = %v, want %v", err, session.ErrMethodAnswer)
	}
}

// Layer 2: a header member that names or holds a key is refused, so the
// key of a token always comes from the key URL.
func TestAHeaderMemberThatCarriesAKeyIsErrMethodAnswer(t *testing.T) {
	t.Parallel()

	keys := newIssuerKeys(t, es256)

	for _, s := range []jwtSetup{
		{preset: headerauth.PresetCustom, alg: es256, claim: ""},
		{preset: headerauth.PresetAWSALB, alg: es256, claim: ""},
	} {
		f := jwtFixture(s, keys)(t)

		for _, member := range []string{"jku", "jwk", "x5u", "x5c", "crit"} {
			h := f.JWT.Header()
			h[member] = "https://evil.example.com/keys"
			raw := f.JWT.Sign(t, h, f.JWT.Claims(alice()))

			_, err := f.Method.FromRequest(t.Context(), facts(map[string][]string{f.UserHeader: {raw}}))
			if !errors.Is(err, session.ErrMethodAnswer) {
				t.Fatalf("%s %s: FromRequest() = %v, want %v", s.preset, member, err, session.ErrMethodAnswer)
			}
		}
	}
}

// Layer 3: a token of Google IAP or a custom issuer must hold iat; the AWS
// ALB sends none.
func TestTheIssuedAtClaimIsRequiredWhereTheIssuerSendsIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		setup jwtSetup
		want  error
	}{
		{setup: jwtSetup{preset: headerauth.PresetGoogleIAP, alg: es256, claim: ""}, want: session.ErrMethodAnswer},
		{setup: jwtSetup{preset: headerauth.PresetCustom, alg: edDSA, claim: ""}, want: session.ErrMethodAnswer},
		{setup: jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""}, want: nil},
	} {
		f := jwtFixture(tc.setup, newIssuerKeys(t, tc.setup.alg))(t)
		c := f.JWT.Claims(alice())
		delete(c, "iat")
		raw := f.JWT.Sign(t, f.JWT.Header(), c)

		_, err := f.Method.FromRequest(t.Context(), facts(map[string][]string{f.UserHeader: {raw}}))
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: FromRequest() = %v, want %v", tc.setup.preset, err, tc.want)
		}
	}
}

// Layer 3: a secret header longer than the limit is refused before the
// compare.
func TestASecretHeaderOverTheLimitIsErrUntrustedSource(t *testing.T) {
	t.Parallel()

	m := newMethod(t, plainConfig(&atomic.Bool{}, ""))

	for _, v := range []string{strings.Repeat(sharedSecret, 1<<12), sharedSecret + "\x00"} {
		f := facts(map[string][]string{"X-Auth-User": {"carol"}, "X-Proxy-Secret": {v}})

		if _, err := m.FromRequest(t.Context(), f); !errors.Is(err, session.ErrUntrustedSource) {
			t.Fatalf("FromRequest() = %v, want %v", err, session.ErrUntrustedSource)
		}
	}
}

// Layer 3: the key cache reads the key URL once while its keys are fresh,
// and a token with an unknown key ID reads it again at most once in
// KeyRetryAfter.
func TestTheKeyCacheLimitsTheReadsOfTheKeyURL(t *testing.T) {
	t.Parallel()

	var now atomic.Int64

	now.Store(fixedNow().UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }

	good := newSigner(t, es256)

	set, err := json.Marshal(map[string]any{"keys": []any{jwkOf(t, good.public, keyID, es256)}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	setup := jwtSetup{preset: headerauth.PresetCustom, alg: es256, claim: ""}
	ks := newKeyServer(t, map[string][]byte{"/jwks": set})
	cfg := jwtConfig(setup, ks, &atomic.Bool{})
	cfg.HTTPClient = ks.client(t, loopback())
	cfg.Now = clock
	m := newMethod(t, cfg)

	shape := jwtShapes(setup)
	send := func(kid string) error {
		h := shape.header()
		h["kid"] = kid
		raw := encodeToken(t, good, h, shape.claims(alice()), base64.RawURLEncoding)
		_, err := m.FromRequest(t.Context(), facts(map[string][]string{shape.user: {raw}}))

		return err
	}

	for range 3 {
		if err := send(keyID); err != nil {
			t.Fatalf("FromRequest() = %v", err)
		}
	}

	for range 3 {
		if err := send("unknown"); !errors.Is(err, session.ErrMethodAnswer) {
			t.Fatalf("FromRequest() with an unknown key = %v, want %v", err, session.ErrMethodAnswer)
		}
	}

	// The read of the first request is less than KeyRetryAfter old, so an
	// unknown key reads nothing.
	if got := len(ks.requests()); got != 1 {
		t.Fatalf("the key URL got %d requests, want 1", got)
	}

	now.Add(int64(headerauth.KeyRetryAfter))

	if err := send("unknown"); !errors.Is(err, session.ErrMethodAnswer) {
		t.Fatalf("FromRequest() = %v, want %v", err, session.ErrMethodAnswer)
	}

	if got := len(ks.requests()); got != 2 {
		t.Fatalf("the key URL got %d requests, want 2", got)
	}
}

// alice is a person of the hostile tests.
func alice() sessiontest.TestPerson {
	return sessiontest.TestPerson{
		AuthTime: time.Time{}, Subject: "alice", UserName: "alice", Name: "Alice", Email: "alice@example.com",
		Groups: nil, GroupsPresent: false, Disabled: false,
	}
}
