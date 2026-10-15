package oauthtest_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
)

const (
	clientID     = "dustpan"
	clientSecret = "client-secret-for-tests"
	redirectURI  = "https://dustpan.example/api/server/sign-in/corp/callback"
	verifier     = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	nonce        = "n-0S6_WzA2Mj-rG7k3QpL"
)

// alice is the person whom the providers of the tests sign in.
func alice() oauthtest.User {
	return oauthtest.User{
		Subject: "248289761001", Name: "Alice Doe", PreferredUsername: "alice", Email: "alice@example.com",
	}
}

func newProvider(t *testing.T, mode oauthtest.Mode, alg string) *oauthtest.Provider {
	t.Helper()

	var none oauthtest.Faults

	return oauthtest.New(t, oauthtest.Config{
		Now: nil, ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirectURI, Algorithm: alg,
		User: alice(), Mode: mode, Faults: none, TLS: true,
	})
}

// Typed views of the answers of a provider.
type (
	discoveryDoc struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`                //nolint:tagliatelle // The OAuth 2.0 wire name; it must not change.
		TokenEndpoint         string   `json:"token_endpoint"`                        //nolint:tagliatelle // The OAuth 2.0 wire name; it must not change.
		UserinfoEndpoint      string   `json:"userinfo_endpoint"`                     //nolint:tagliatelle // The OAuth 2.0 wire name; it must not change.
		Algorithms            []string `json:"id_token_signing_alg_values_supported"` //nolint:tagliatelle // The OAuth 2.0 wire name; it must not change.
	}
	tokenAnswer struct {
		IDToken     *string `json:"id_token"`     //nolint:tagliatelle // The OAuth 2.0 wire name; it must not change.
		AccessToken string  `json:"access_token"` //nolint:tagliatelle // The OAuth 2.0 wire name; it must not change.
		Error       string  `json:"error"`
	}
	userinfoDoc struct {
		Subject string `json:"sub"`
		Name    string `json:"name"`
	}
	jwkDoc struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	jwksDoc struct {
		Keys []jwkDoc `json:"keys"`
	}
	jwsHeader struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	idClaims struct {
		Issuer   string `json:"iss"`
		Audience string `json:"aud"`
		Nonce    string `json:"nonce"`
		Subject  string `json:"sub"`
		Email    string `json:"email"`
		Expiry   int64  `json:"exp"`
	}
)

// challenge returns the S256 challenge of the verifier v.
func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// closeBody closes the body of resp, and fails the test on an error.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
}

// authorize sends an authorization request with the extra parameters and
// returns the query of the redirect back to the client.
func authorize(t *testing.T, p *oauthtest.Provider, extra url.Values) url.Values {
	t.Helper()

	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"scope": {"openid profile email"}, "state": {"state-1"}, "nonce": {nonce},
		"code_challenge": {challenge(verifier)}, "code_challenge_method": {"S256"},
	}
	maps.Copy(q, extra)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.AuthorizationEndpoint()+"?"+q.Encode(),
		http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	client := *p.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}

	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize answers %d, want a redirect", resp.StatusCode)
	}

	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}

	if !strings.HasPrefix(loc.String(), redirectURI+"?") {
		t.Fatalf("redirect to %q, want the redirect URI", loc)
	}

	return loc.Query()
}

// exchange sends a token request for code with the verifier v and the
// client secret secret, and returns the status and the answer.
func exchange(t *testing.T, p *oauthtest.Provider, code, v, secret string) (int, tokenAnswer) {
	t.Helper()

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {v},
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, p.TokenEndpoint(),
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))

	var answer tokenAnswer

	status := send(t, p, req, &answer)

	return status, answer
}

// send sends req, decodes a JSON answer into out, and returns the status.
// An error answer can be plain text, as from a real provider.
func send(t *testing.T, p *oauthtest.Provider, req *http.Request, out any) int {
	t.Helper()

	resp, err := p.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}

	defer closeBody(t, resp)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}

	if err := json.Unmarshal(raw, out); err != nil && resp.StatusCode < http.StatusBadRequest {
		t.Fatalf("decode answer %q: %v", raw, err)
	}

	return resp.StatusCode
}

// get sends a GET to rawURL with an optional bearer token, decodes the
// answer into out, and returns the status.
func get(t *testing.T, p *oauthtest.Provider, rawURL, bearer string, out any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	return send(t, p, req, out)
}

// b64 decodes one unpadded base64url member.
func b64(t *testing.T, s string) []byte {
	t.Helper()

	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}

	return b
}

// publicKey reads the one key of the JWKS of p.
func publicKey(t *testing.T, p *oauthtest.Provider) (crypto.PublicKey, string) {
	t.Helper()

	var doc jwksDoc
	if status := get(t, p, p.Issuer()+"/jwks", "", &doc); status != http.StatusOK || len(doc.Keys) != 1 {
		t.Fatalf("jwks answers %d with %d keys, want one key", status, len(doc.Keys))
	}

	k := doc.Keys[0]

	switch k.Kty {
	case "RSA":
		e := new(big.Int).SetBytes(b64(t, k.E))

		return &rsa.PublicKey{N: new(big.Int).SetBytes(b64(t, k.N)), E: int(e.Int64())}, k.Kid
	case "EC":
		point := append([]byte{4}, b64(t, k.X)...)

		ec, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(point, b64(t, k.Y)...))
		if err != nil {
			t.Fatalf("parse EC key: %v", err)
		}

		return ec, k.Kid
	case "OKP":
		return ed25519.PublicKey(b64(t, k.X)), k.Kid
	default:
		t.Fatalf("jwk kty = %q", k.Kty)

		return nil, ""
	}
}

// es256Half is the size of R and of S in an ES256 signature.
const es256Half = 32

// decoded is a compact JWS as verify reads it: its header, its claims, and
// whether its signature checks.
type decoded struct {
	header jwsHeader
	claims idClaims
	valid  bool
}

// verify decodes the compact JWS token and checks its signature with pub
// for alg.
func verify(t *testing.T, token string, pub crypto.PublicKey, alg string) decoded {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}

	var (
		header jwsHeader
		claims idClaims
	)

	if err := json.Unmarshal(b64(t, parts[0]), &header); err != nil {
		t.Fatalf("header: %v", err)
	}

	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatalf("claims: %v", err)
	}

	sig := b64(t, parts[2])
	input := []byte(parts[0] + "." + parts[1])
	sum := sha256.Sum256(input)

	out := decoded{header: header, claims: claims, valid: false}

	switch k := pub.(type) {
	case *rsa.PublicKey:
		out.valid = alg == oauthtest.RS256 && rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig) == nil
	case *ecdsa.PublicKey:
		out.valid = alg == oauthtest.ES256 && len(sig) == 2*es256Half &&
			ecdsa.Verify(k, sum[:], new(big.Int).SetBytes(sig[:es256Half]), new(big.Int).SetBytes(sig[es256Half:]))
	case ed25519.PublicKey:
		out.valid = alg == oauthtest.EdDSA && ed25519.Verify(k, input, sig)
	default:
		t.Fatalf("key type %T", pub)
	}

	return out
}

func TestDiscovery(t *testing.T) {
	t.Parallel()

	p := newProvider(t, oauthtest.ModeOIDC, oauthtest.ES256)

	var doc discoveryDoc
	if status := get(t, p, p.Issuer()+"/.well-known/openid-configuration", "", &doc); status != http.StatusOK {
		t.Fatalf("discovery answers %d", status)
	}

	want := discoveryDoc{
		Issuer: p.Issuer(), AuthorizationEndpoint: p.AuthorizationEndpoint(), TokenEndpoint: p.TokenEndpoint(),
		UserinfoEndpoint: p.UserinfoEndpoint(), Algorithms: []string{oauthtest.ES256},
	}
	if doc.Issuer != want.Issuer || doc.AuthorizationEndpoint != want.AuthorizationEndpoint ||
		doc.TokenEndpoint != want.TokenEndpoint || doc.UserinfoEndpoint != want.UserinfoEndpoint ||
		len(doc.Algorithms) != 1 || doc.Algorithms[0] != oauthtest.ES256 {
		t.Fatalf("discovery = %+v, want %+v", doc, want)
	}

	o := newProvider(t, oauthtest.ModeOAuth, "")

	var none discoveryDoc
	if status := get(t, o, o.Issuer()+"/.well-known/openid-configuration", "", &none); status != http.StatusNotFound {
		t.Fatalf("discovery of a plain OAuth provider answers %d, want 404", status)
	}
}

func TestCodeFlow(t *testing.T) {
	t.Parallel()

	for _, alg := range []string{oauthtest.RS256, oauthtest.ES256, oauthtest.EdDSA} {
		t.Run(alg, func(t *testing.T) {
			t.Parallel()
			checkCodeFlow(t, alg)
		})
	}
}

// checkCodeFlow runs one sign-in with a provider that signs with alg.
func checkCodeFlow(t *testing.T, alg string) {
	t.Helper()

	p := newProvider(t, oauthtest.ModeOIDC, alg)
	back := authorize(t, p, nil)

	if back.Get("state") != "state-1" || back.Get("code") == "" {
		t.Fatalf("redirect query = %v, want the state and a code", back)
	}

	status, answer := exchange(t, p, back.Get("code"), verifier, clientSecret)
	if status != http.StatusOK || answer.IDToken == nil {
		t.Fatalf("token answers %d %+v, want an ID token", status, answer)
	}

	checkIDToken(t, p, *answer.IDToken, alg)

	var info userinfoDoc
	if status := get(t, p, p.UserinfoEndpoint(), answer.AccessToken, &info); status != http.StatusOK ||
		info.Subject != alice().Subject || info.Name != alice().Name {
		t.Fatalf("userinfo answers %d: %+v", status, info)
	}
}

// checkIDToken checks the signature, the header and the claims of the ID
// token of a sign-in with p, which signs with alg.
func checkIDToken(t *testing.T, p *oauthtest.Provider, token, alg string) {
	t.Helper()

	pub, kid := publicKey(t, p)

	d := verify(t, token, pub, alg)
	if !d.valid {
		t.Fatal("the signature of the ID token does not check")
	}

	if d.header.Alg != alg || d.header.Kid != kid {
		t.Fatalf("header = %+v, want alg %s and kid %s", d.header, alg, kid)
	}

	c := d.claims
	if c.Issuer != p.Issuer() || c.Audience != clientID || c.Nonce != nonce || c.Subject != alice().Subject ||
		c.Email != alice().Email {
		t.Fatalf("claims = %+v", c)
	}
}

func TestTokenRefusals(t *testing.T) {
	t.Parallel()

	p := newProvider(t, oauthtest.ModeOIDC, oauthtest.RS256)

	code := authorize(t, p, nil).Get("code")
	if status, answer := exchange(t, p, code, strings.Repeat("a", 43), clientSecret); status != http.StatusBadRequest ||
		answer.Error != "invalid_grant" {
		t.Fatalf("token with a wrong verifier answers %d %+v, want invalid_grant", status, answer)
	}

	if status, _ := exchange(t, p, code, verifier, clientSecret); status != http.StatusBadRequest {
		t.Fatalf("second use of a code answers %d, want 400", status)
	}

	code = authorize(t, p, nil).Get("code")
	if status, answer := exchange(t, p, code, verifier, "wrong"); status != http.StatusUnauthorized ||
		answer.Error != "invalid_client" {
		t.Fatalf("token with a wrong secret answers %d %+v, want invalid_client", status, answer)
	}

	var info userinfoDoc
	if status := get(t, p, p.UserinfoEndpoint(), "unknown", &info); status != http.StatusUnauthorized {
		t.Fatalf("userinfo with an unknown token answers %d, want 401", status)
	}

	back := authorize(t, p, url.Values{"code_challenge_method": {"plain"}})
	if back.Get("error") != "invalid_request" || back.Get("code") != "" {
		t.Fatalf("authorize without S256 = %v, want invalid_request and no code", back)
	}
}

func TestReauthentication(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC)

	var none oauthtest.Faults

	p := oauthtest.New(t, oauthtest.Config{
		Now: func() time.Time { return now }, ClientID: clientID, ClientSecret: clientSecret,
		RedirectURI: redirectURI, Algorithm: "", User: alice(), Mode: oauthtest.ModeOIDC, Faults: none, TLS: false,
	})

	authorize(t, p, nil)
	authorize(t, p, nil)

	if got := p.SignIns(); got != 1 {
		t.Fatalf("sign-ins after two requests = %d, want 1: the provider keeps the first", got)
	}

	authorize(t, p, url.Values{"prompt": {"login"}})

	if got := p.SignIns(); got != 2 {
		t.Fatalf("sign-ins after prompt=login = %d, want 2", got)
	}

	authorize(t, p, url.Values{"max_age": {"0"}})

	if got := p.SignIns(); got != 2 {
		t.Fatalf("sign-ins after max_age=0 at the same time = %d, want 2", got)
	}

	if q := p.LastAuthorizationQuery(); q.Get("max_age") != "0" {
		t.Fatalf("last query = %v, want max_age=0", q)
	}
}

func TestPlainOAuthHasNoIDToken(t *testing.T) {
	t.Parallel()

	p := newProvider(t, oauthtest.ModeOAuth, "")

	back := authorize(t, p, url.Values{"scope": {"read:user"}, "nonce": nil})

	status, answer := exchange(t, p, back.Get("code"), verifier, clientSecret)
	if status != http.StatusOK || answer.IDToken != nil {
		t.Fatalf("token answers %d %+v, want no ID token", status, answer)
	}

	var info userinfoDoc
	if status := get(t, p, p.UserinfoEndpoint(), answer.AccessToken, &info); status != http.StatusOK ||
		info.Subject != alice().Subject {
		t.Fatalf("userinfo answers %d: %+v", status, info)
	}
}

func TestFaults(t *testing.T) {
	t.Parallel()

	var badSignature, unknownKey, algNone, claims oauthtest.Faults

	badSignature.BadSignature = true
	unknownKey.UnknownKey = true
	algNone.AlgNone = true
	claims.WrongIssuer, claims.WrongAudience, claims.WrongNonce, claims.Expired = true, true, true, true

	cases := []struct {
		check  func(t *testing.T, d decoded)
		name   string
		faults oauthtest.Faults
	}{
		{name: "bad signature", faults: badSignature, check: wantBadSignature},
		{name: "unknown key", faults: unknownKey, check: wantBadSignature},
		{name: "alg none", faults: algNone, check: wantAlgNone},
		{name: "claims", faults: claims, check: wantWrongClaims},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newProvider(t, oauthtest.ModeOIDC, oauthtest.RS256)
			p.SetFaults(tc.faults)

			code := authorize(t, p, nil).Get("code")

			status, answer := exchange(t, p, code, verifier, clientSecret)
			if status != http.StatusOK || answer.IDToken == nil {
				t.Fatalf("token answers %d %+v, want an ID token", status, answer)
			}

			pub, _ := publicKey(t, p)
			tc.check(t, verify(t, *answer.IDToken, pub, oauthtest.RS256))
		})
	}
}

func wantBadSignature(t *testing.T, d decoded) {
	t.Helper()

	if d.valid {
		t.Fatal("the signature checks, want a bad signature")
	}
}

func wantAlgNone(t *testing.T, d decoded) {
	t.Helper()

	if d.header.Alg != "none" || d.valid {
		t.Fatalf("decoded = %+v: want alg none and no valid signature", d)
	}
}

func wantWrongClaims(t *testing.T, d decoded) {
	t.Helper()

	c := d.claims
	if !d.valid || c.Issuer == "" || c.Audience == clientID || c.Nonce == nonce || c.Expiry > time.Now().Unix() {
		t.Fatalf("decoded = %+v: want a valid signature on wrong claims", d)
	}
}
