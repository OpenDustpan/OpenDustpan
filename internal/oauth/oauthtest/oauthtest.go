// Package oauthtest is an OpenID provider for tests. It serves discovery,
// a JWKS, an authorization endpoint that signs in one configured user with
// no page, a token endpoint with a PKCE check, and a user-info endpoint. In
// the OAuth mode it serves no discovery document and no ID token, as a
// plain OAuth 2.0 provider does. The unit tests and the e2e harness use it,
// so the server never needs a real identity provider in a test.
//
// The provider follows the parts of OpenID Connect Core 1.0, RFC 6749 and
// RFC 7636 that a confidential client with the authorization code flow
// uses. Faults make it answer what a broken or hostile provider answers, so
// a test can prove that the client refuses it.
package oauthtest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mode names what kind of provider the server is.
type Mode int

// Modes of a provider.
const (
	// ModeOIDC is an OpenID provider: discovery, a JWKS and an ID token.
	ModeOIDC Mode = iota
	// ModeOAuth is a plain OAuth 2.0 provider: no discovery and no ID
	// token. A client finds the person through the user-info endpoint.
	ModeOAuth
)

// Algorithms of the ID token signature.
const (
	RS256 = "RS256"
	ES256 = "ES256"
	EdDSA = "EdDSA"
)

// Faults make the provider break one rule, so a test can prove that a
// client refuses the answer. The zero value breaks nothing.
type Faults struct {
	// WrongIssuer signs an ID token with another iss.
	WrongIssuer bool
	// WrongAudience signs an ID token for another client.
	WrongAudience bool
	// WrongNonce signs an ID token with another nonce.
	WrongNonce bool
	// Expired signs an ID token that expired one hour ago.
	Expired bool
	// BadSignature changes one byte of the signature.
	BadSignature bool
	// AlgNone answers an unsigned ID token with the alg "none".
	AlgNone bool
	// UnknownKey signs with a key that the JWKS does not hold.
	UnknownKey bool
	// SkipPKCE accepts a token request with a wrong code verifier.
	SkipPKCE bool
	// ExtraAudience signs an ID token for the client and for another
	// client, so the aud claim is a list of two.
	ExtraAudience bool
	// AuthorizedParty sets the azp claim of an ID token when it is not
	// empty.
	AuthorizedParty string
	// IssuedIn moves the iat claim of an ID token by this duration from
	// now, and its exp claim with it, when it is not zero.
	IssuedIn time.Duration
}

// User is the person whom the provider signs in.
type User struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
}

// Config configures a provider. ClientID and ClientSecret are the one
// client that the provider knows. RedirectURI is the one redirect URI that
// it accepts. Algorithm is RS256 (the default), ES256 or EdDSA. Now is the
// clock, time.Now when nil. TLS serves HTTPS with the certificate of
// httptest, which Client trusts.
type Config struct {
	Now          func() time.Time
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Algorithm    string
	User         User
	Mode         Mode
	Faults       Faults
	TLS          bool
}

// Errors of the key setup.
var (
	// ErrUnknownAlgorithm reports an algorithm that the provider does not sign with.
	ErrUnknownAlgorithm = errors.New("unknown signature algorithm")
	// ErrUnknownKey reports a key type that the provider cannot publish.
	ErrUnknownKey = errors.New("unknown key type")
)

// Names of the claims about the person.
const (
	claimSubject           = "sub"
	claimName              = "name"
	claimPreferredUsername = "preferred_username"
	claimEmail             = "email"
	claimAuthTime          = "auth_time"
)

// claims returns the claims about u that are set.
func (u User) claims() map[string]any {
	out := map[string]any{claimSubject: u.Subject}

	for name, value := range map[string]string{
		claimName: u.Name, claimPreferredUsername: u.PreferredUsername, claimEmail: u.Email,
	} {
		if value != "" {
			out[name] = value
		}
	}

	return out
}

// tokenLifetime is how long an ID token and an access token of the
// provider are valid.
const tokenLifetime = 5 * time.Minute

// codeLifetime is how long an authorization code is valid.
const codeLifetime = time.Minute

// rsaBits is the size of the RSA key.
const rsaBits = 2048

// grant is one authorization code with what the token endpoint needs.
type grant struct {
	issued      time.Time
	authTime    time.Time
	user        User
	nonce       string
	challenge   string
	redirectURI string
}

// Provider is a running test provider.
type Provider struct {
	server    *httptest.Server
	cfg       Config
	signer    crypto.Signer
	foreign   crypto.Signer
	keyID     string
	mu        sync.Mutex
	codes     map[string]grant
	tokens    map[string]User
	authTime  time.Time
	signIns   int
	lastQuery url.Values
}

// New starts a provider and stops it when the test ends.
func New(tb testing.TB, cfg Config) *Provider {
	tb.Helper()

	if cfg.Algorithm == "" {
		cfg.Algorithm = RS256
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	signer, err := newKey(cfg.Algorithm)
	if err != nil {
		tb.Fatalf("oauthtest: %v", err)
	}

	foreign, err := newKey(cfg.Algorithm)
	if err != nil {
		tb.Fatalf("oauthtest: %v", err)
	}

	p := &Provider{
		server:    nil,
		cfg:       cfg,
		signer:    signer,
		foreign:   foreign,
		keyID:     "key-" + randomText(),
		mu:        sync.Mutex{},
		codes:     map[string]grant{},
		tokens:    map[string]User{},
		authTime:  time.Time{},
		signIns:   0,
		lastQuery: nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /userinfo", p.userinfo)

	if cfg.TLS {
		p.server = httptest.NewTLSServer(mux)
	} else {
		p.server = httptest.NewServer(mux)
	}

	tb.Cleanup(p.server.Close)

	return p
}

// newKey returns a new private key for alg.
func newKey(alg string) (crypto.Signer, error) {
	switch alg {
	case RS256:
		k, err := rsa.GenerateKey(rand.Reader, rsaBits)
		if err != nil {
			return nil, fmt.Errorf("generate RSA key: %w", err)
		}

		return k, nil
	case ES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ECDSA key: %w", err)
		}

		return k, nil
	case EdDSA:
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate Ed25519 key: %w", err)
		}

		return k, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, alg)
	}
}

// Issuer returns the issuer URL: the base URL of the provider.
func (p *Provider) Issuer() string {
	return p.server.URL
}

// Client returns an HTTP client that trusts the provider.
func (p *Provider) Client() *http.Client {
	return p.server.Client()
}

// AuthorizationEndpoint returns the URL of the authorization endpoint.
func (p *Provider) AuthorizationEndpoint() string {
	return p.server.URL + "/authorize"
}

// TokenEndpoint returns the URL of the token endpoint.
func (p *Provider) TokenEndpoint() string {
	return p.server.URL + "/token"
}

// UserinfoEndpoint returns the URL of the user-info endpoint.
func (p *Provider) UserinfoEndpoint() string {
	return p.server.URL + "/userinfo"
}

// SetUser changes the person whom the next sign-in signs in.
func (p *Provider) SetUser(u User) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cfg.User = u
}

// SetFaults changes the faults of the next answers.
func (p *Provider) SetFaults(f Faults) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cfg.Faults = f
}

// SignIns returns how many times the provider authenticated the person:
// the first authorization request, and each one with prompt=login or with
// a max_age that the last authentication exceeds.
func (p *Provider) SignIns() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.signIns
}

// LastAuthorizationQuery returns the query of the last authorization
// request, so a test can check what a client sent.
func (p *Provider) LastAuthorizationQuery() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.lastQuery
}

// discovery answers the OpenID provider metadata. A provider in the OAuth
// mode has none.
func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	if p.cfg.Mode == ModeOAuth {
		http.NotFound(w, nil)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.Issuer(),
		"authorization_endpoint":                p.AuthorizationEndpoint(),
		"token_endpoint":                        p.TokenEndpoint(),
		"userinfo_endpoint":                     p.UserinfoEndpoint(),
		"jwks_uri":                              p.server.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{p.cfg.Algorithm},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"claims_supported": []string{
			claimSubject, claimName, claimPreferredUsername, claimEmail, claimAuthTime,
		},
	})
}

// jwks answers the public key of the provider.
func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	jwk, err := publicJWK(p.signer.Public(), p.cfg.Algorithm, p.keyID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"keys": []any{jwk}})
}

// Sizes of an ES256 key and signature: the X and Y coordinates and the R
// and S values are 32 bytes each.
const (
	es256Coord     = 32
	es256Signature = 2 * es256Coord
)

// publicJWK returns the JWK of the public key pub (RFC 7517, RFC 7518,
// RFC 8037).
func publicJWK(pub crypto.PublicKey, alg, kid string) (map[string]string, error) {
	enc := base64.RawURLEncoding.EncodeToString

	switch k := pub.(type) {
	case *rsa.PublicKey:
		return jwk("RSA", alg, kid, map[string]string{
			"n": enc(k.N.Bytes()), "e": enc(big.NewInt(int64(k.E)).Bytes()),
		}), nil
	case *ecdsa.PublicKey:
		raw, err := k.Bytes()
		if err != nil {
			return nil, fmt.Errorf("encode ECDSA key: %w", err)
		}

		// The uncompressed point is 0x04, then X and Y.
		return jwk("EC", alg, kid, map[string]string{
			"crv": "P-256", "x": enc(raw[1 : 1+es256Coord]), "y": enc(raw[1+es256Coord:]),
		}), nil
	case ed25519.PublicKey:
		// The curve name of RFC 8037 is the name of the TLS signature scheme.
		return jwk("OKP", alg, kid, map[string]string{"crv": tls.Ed25519.String(), "x": enc(k)}), nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnknownKey, pub)
	}
}

// jwk returns a signature JWK of the key type kty with the members of the
// key.
func jwk(kty, alg, kid string, key map[string]string) map[string]string {
	out := map[string]string{"kty": kty, "use": "sig", "alg": alg, "kid": kid}
	maps.Copy(out, key)

	return out
}

// authorize checks an authorization request and sends the browser back to
// the client with a code. It signs the person in again when the request
// asks for it with prompt=login, or with a max_age that the last
// authentication exceeds.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	p.mu.Lock()
	p.lastQuery = q
	p.mu.Unlock()

	if q.Get("client_id") != p.cfg.ClientID || q.Get("redirect_uri") != p.cfg.RedirectURI {
		http.Error(w, "unknown client or redirect URI", http.StatusBadRequest)

		return
	}

	redirect, err := url.Parse(p.cfg.RedirectURI)
	if err != nil {
		http.Error(w, "bad redirect URI", http.StatusBadRequest)

		return
	}

	back := url.Values{"state": {q.Get("state")}}

	if problem := checkAuthorization(q, p.cfg.Mode); problem != "" {
		back.Set("error", "invalid_request")
		back.Set("error_description", problem)
		redirect.RawQuery = back.Encode()
		redirectTo(w, redirect)

		return
	}

	code := base64.RawURLEncoding.EncodeToString(randomBytes())

	p.mu.Lock()
	now := p.cfg.Now()

	if p.mustSignIn(q, now) {
		p.authTime = now
		p.signIns++
	}

	p.codes[code] = grant{
		issued: now, authTime: p.authTime, user: p.cfg.User, nonce: q.Get("nonce"),
		challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri"),
	}
	p.mu.Unlock()

	back.Set("code", code)
	redirect.RawQuery = back.Encode()
	redirectTo(w, redirect)
}

// redirectTo sends the browser to target: the configured redirect URI of
// the client with the answer in its query. The request only has to name
// that URI exactly, so it never chooses the host.
func redirectTo(w http.ResponseWriter, target *url.URL) {
	w.Header().Set("Location", target.String())
	w.WriteHeader(http.StatusFound)
}

// checkAuthorization returns why the authorization query q is wrong, or
// "".
func checkAuthorization(q url.Values, mode Mode) string {
	switch {
	case q.Get("response_type") != "code":
		return "response_type must be code"
	case q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		return "PKCE with S256 is required"
	case q.Get("state") == "":
		return "state is required"
	case mode == ModeOIDC && !hasScope(q.Get("scope"), "openid"):
		return "the scope must hold openid"
	case mode == ModeOIDC && q.Get("nonce") == "":
		return "nonce is required"
	default:
		return ""
	}
}

// mustSignIn reports whether the authorization query q needs a new
// authentication at now. The caller holds p.mu.
func (p *Provider) mustSignIn(q url.Values, now time.Time) bool {
	if p.authTime.IsZero() || q.Get("prompt") == "login" {
		return true
	}

	raw := q.Get("max_age")
	if raw == "" {
		return false
	}

	var seconds int64
	if _, err := fmt.Sscanf(raw, "%d", &seconds); err != nil || seconds < 0 {
		return true
	}

	return now.Sub(p.authTime) > time.Duration(seconds)*time.Second
}

// hasScope reports whether the space-separated scope list holds want.
func hasScope(scopes, want string) bool {
	for s := range strings.FieldsSeq(scopes) {
		if s == want {
			return true
		}
	}

	return false
}

// token exchanges an authorization code for an access token and, in the
// OIDC mode, an ID token.
func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")

		return
	}

	if !p.clientAuthenticated(r) {
		tokenError(w, http.StatusUnauthorized, "invalid_client")

		return
	}

	if r.PostForm.Get("grant_type") != "authorization_code" {
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type")

		return
	}

	p.mu.Lock()
	code := r.PostForm.Get("code")
	g, ok := p.codes[code]
	delete(p.codes, code)
	faults := p.cfg.Faults
	now := p.cfg.Now()
	p.mu.Unlock()

	if !ok || now.Sub(g.issued) > codeLifetime || r.PostForm.Get("redirect_uri") != g.redirectURI {
		tokenError(w, http.StatusBadRequest, "invalid_grant")

		return
	}

	if !faults.SkipPKCE && !pkceMatches(r.PostForm.Get("code_verifier"), g.challenge) {
		tokenError(w, http.StatusBadRequest, "invalid_grant")

		return
	}

	access := base64.RawURLEncoding.EncodeToString(randomBytes())

	p.mu.Lock()
	p.tokens[access] = g.user
	p.mu.Unlock()

	body := map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   int64(tokenLifetime / time.Second),
	}

	if p.cfg.Mode == ModeOIDC {
		idToken, err := p.idToken(g, now, faults)
		if err != nil {
			tokenError(w, http.StatusInternalServerError, "server_error")

			return
		}

		body["id_token"] = idToken
	}

	writeJSON(w, http.StatusOK, body)
}

// clientAuthenticated reports whether r carries the client credentials,
// with client_secret_basic or client_secret_post.
func (p *Provider) clientAuthenticated(r *http.Request) bool {
	id, secret, ok := r.BasicAuth()
	if ok {
		// RFC 6749 section 2.3.1: the basic credentials are form-encoded.
		var idErr, secretErr error

		id, idErr = url.QueryUnescape(id)
		secret, secretErr = url.QueryUnescape(secret)

		if idErr != nil || secretErr != nil {
			return false
		}
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}

	return subtle.ConstantTimeCompare([]byte(id), []byte(p.cfg.ClientID)) == 1 &&
		subtle.ConstantTimeCompare([]byte(secret), []byte(p.cfg.ClientSecret)) == 1
}

// pkceMatches reports whether verifier gives challenge with S256.
func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))

	return verifier != "" &&
		subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// idToken returns the signed ID token of the grant g at now, with the
// faults f.
func (p *Provider) idToken(g grant, now time.Time, f Faults) (string, error) {
	claims := g.user.claims()
	claims["iss"] = p.Issuer()
	claims["aud"] = p.cfg.ClientID
	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(tokenLifetime).Unix()
	claims[claimAuthTime] = g.authTime.Unix()
	claims["nonce"] = g.nonce

	applyClaimFaults(claims, f, now)

	return p.sign(claims, f)
}

// applyClaimFaults breaks the claims as f asks.
func applyClaimFaults(claims map[string]any, f Faults, now time.Time) {
	if f.WrongIssuer {
		claims["iss"] = "https://other-issuer.example"
	}

	if f.WrongAudience {
		claims["aud"] = "another-client"
	}

	if f.WrongNonce {
		claims["nonce"] = "another-nonce"
	}

	if f.ExtraAudience {
		claims["aud"] = []any{claims["aud"], "another-client"}
	}

	if f.AuthorizedParty != "" {
		claims["azp"] = f.AuthorizedParty
	}

	if f.IssuedIn != 0 {
		claims["iat"] = now.Add(f.IssuedIn).Unix()
		claims["exp"] = now.Add(f.IssuedIn + tokenLifetime).Unix()
	}

	if f.Expired {
		claims["iat"] = now.Add(-2 * time.Hour).Unix()
		claims["exp"] = now.Add(-time.Hour).Unix()
	}
}

// sign returns the compact JWS of claims (RFC 7515), signed with the key
// of the provider, or broken as f asks.
func (p *Provider) sign(claims map[string]any, f Faults) (string, error) {
	alg := p.cfg.Algorithm
	if f.AlgNone {
		alg = "none"
	}

	header, err := json.Marshal(map[string]string{"alg": alg, "typ": "JWT", "kid": p.keyID})
	if err != nil {
		return "", fmt.Errorf("encode header: %w", err)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode claims: %w", err)
	}

	enc := base64.RawURLEncoding.EncodeToString
	input := enc(header) + "." + enc(payload)

	if f.AlgNone {
		return input + ".", nil
	}

	key := p.signer
	if f.UnknownKey {
		key = p.foreign
	}

	sig, err := signInput(key, p.cfg.Algorithm, []byte(input))
	if err != nil {
		return "", err
	}

	if f.BadSignature {
		sig[0] ^= 0xff
	}

	return input + "." + enc(sig), nil
}

// signInput signs input with key for alg, in the JWS form of the
// signature (RFC 7518 section 3.4 for ES256: R and S of 32 bytes each).
func signInput(key crypto.Signer, alg string, input []byte) ([]byte, error) {
	switch alg {
	case RS256:
		sum := sha256.Sum256(input)

		sig, err := key.Sign(rand.Reader, sum[:], crypto.SHA256)
		if err != nil {
			return nil, fmt.Errorf("sign RS256: %w", err)
		}

		return sig, nil
	case ES256:
		ec, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: ES256 needs an ECDSA key, not %T", ErrUnknownKey, key)
		}

		sum := sha256.Sum256(input)

		r, s, err := ecdsa.Sign(rand.Reader, ec, sum[:])
		if err != nil {
			return nil, fmt.Errorf("sign ES256: %w", err)
		}

		sig := make([]byte, es256Signature)
		r.FillBytes(sig[:es256Coord])
		s.FillBytes(sig[es256Coord:])

		return sig, nil
	case EdDSA:
		sig, err := key.Sign(rand.Reader, input, crypto.Hash(0))
		if err != nil {
			return nil, fmt.Errorf("sign EdDSA: %w", err)
		}

		return sig, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, alg)
	}
}

// userinfo answers the claims of the person of a bearer access token.
func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	access, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")

	p.mu.Lock()
	u, known := p.tokens[access]
	p.mu.Unlock()

	if !ok || !known {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	writeJSON(w, http.StatusOK, u.claims())
}

// tokenError writes an OAuth error answer (RFC 6749 section 5.2).
func tokenError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// writeJSON writes body as JSON with status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "encode the answer", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if _, err := w.Write(data); err != nil {
		// The client of the test sees the broken answer.
		return
	}
}

// secretBytes is the size of a code and of an access token: 256 bits.
const secretBytes = 32

// randomBytes returns secretBytes from crypto/rand. crypto/rand.Read never
// returns an error: it ends the program instead.
func randomBytes() []byte {
	b := make([]byte, secretBytes)
	_, _ = rand.Read(b)

	return b
}

// keyIDLength is the length of the random part of a key ID.
const keyIDLength = 8

// randomText returns a short random text for a key ID.
func randomText() string {
	return strings.ToLower(rand.Text()[:keyIDLength])
}
