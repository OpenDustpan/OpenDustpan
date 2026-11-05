package github_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

const (
	fakeAppID          = "4242"
	fakeInstallationID = "9001"
	fakeAccountID      = "777"
	fakeLogin          = "acme"
)

var errNotImplemented = errors.New("the fake GitHub API has no such fixture")

// testKey returns one RSA key for all tests of the package: a key of 2048
// bits takes long to make.
//
//nolint:gochecknoglobals // One key for all tests: a key of 2048 bits takes long to make.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	return key
})

func pkcs1PEM(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)
}

func pkcs8PEM(t *testing.T, key any) []byte {
	t.Helper()

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: nil, Bytes: der})
}

func randomHex(t *testing.T, n int) string {
	t.Helper()

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}

	return hex.EncodeToString(b)
}

// fake is a fake GitHub API on a local TLS server. It checks the JWT of an
// app with the public key, or the token of a user, and answers the three
// calls of Check. It is also the Fixture of the conformance suite.
type fake struct {
	srv   *httptest.Server
	pub   *rsa.PublicKey
	token string

	mu          sync.Mutex
	calls       int
	refused     bool
	unavailable bool
	retryAfter  time.Duration
	garble      bool
	expiry      time.Time
	redirectTo  string
	status      int
	statusHdr   map[string]string
	lastJWT     string
	lastToken   string
	minted      int
	mintBody    string
	revoked     int
}

func newFake(t *testing.T, key *rsa.PrivateKey, userToken string) *fake {
	t.Helper()

	f := &fake{pub: &key.PublicKey, token: userToken} //nolint:exhaustruct_v5 // the other fields keep their zero values
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)

	return f
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	data, err := json.Marshal(v)
	if err != nil {
		return
	}

	_, _ = w.Write(data) //nolint:errcheck // A test server.
}

var errBadJWT = errors.New("bad JWT")

// verifyJWT checks the form, the signature and the claims of a JWT of the
// app.
func verifyJWT(token string, pub *rsa.PublicKey) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errBadJWT
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errBadJWT
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) != nil {
		return errBadJWT
	}

	return nil
}

// Fixture methods of the conformance suite.

func (f *fake) Push(context.Context, string, map[string][]byte) (forge.Commit, error) {
	return forge.Commit{}, errNotImplemented
}

func (f *fake) Rewrite(context.Context, string, map[string][]byte) (forge.Commit, error) {
	return forge.Commit{}, errNotImplemented
}

func (f *fake) Tag(context.Context, string, string) error { return errNotImplemented }

func (f *fake) Protect(context.Context, string) error { return errNotImplemented }

func (f *fake) Merge(context.Context, string) error { return errNotImplemented }

func (f *fake) Event(context.Context, forge.EventKind, []byte) (forge.EventRequest, error) {
	return forge.EventRequest{}, errNotImplemented
}

func (f *fake) Redeliver(context.Context, forge.EventRequest) (forge.EventRequest, error) {
	return forge.EventRequest{}, errNotImplemented
}

func (f *fake) Refuse(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.refused = true

	return nil
}

func (f *fake) Calls(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls, nil
}

func (f *fake) SetTokenExpiry(_ context.Context, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.expiry = at

	return nil
}

func (f *fake) Garble(context.Context) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.garble = true

	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.garble = false
	}, nil
}

func (f *fake) Unavailable(_ context.Context, retryAfter time.Duration) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.unavailable, f.retryAfter = true, retryAfter

	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.unavailable, f.retryAfter = false, 0
	}, nil
}

// respondWith makes the fake answer each call with status and headers,
// before it checks the credential.
func (f *fake) respondWith(status int, headers map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.status, f.statusHdr = status, headers
}

// redirect makes the fake answer each call with a redirect to target.
func (f *fake) redirect(target string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.redirectTo = target
}

// revocation returns the body of the last token request and the number of
// tokens that the adapter revoked.
func (f *fake) revocation() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.mintBody, f.revoked
}

func (f *fake) seen() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.lastJWT, f.minted
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return key
}

func smallKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // A weak key to test that the adapter refuses it.
	if err != nil {
		t.Fatal(err)
	}

	return key
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	if f.redirectTo != "" {
		http.Redirect(w, r, f.redirectTo, http.StatusFound)

		return
	}

	if f.unavailable {
		if f.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(f.retryAfter/time.Second)))
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)

		return
	}

	if f.status != 0 {
		for k, v := range f.statusHdr {
			w.Header().Set(k, v)
		}

		w.WriteHeader(f.status)

		return
	}

	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if f.refused || !f.authorized(r, bearer) {
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	if f.garble {
		_, _ = w.Write([]byte("not json {")) //nolint:errcheck // A test server.

		return
	}

	f.route(w, r, bearer)
}

func (f *fake) route(w http.ResponseWriter, r *http.Request, bearer string) {
	prefix := "/app/installations/" + fakeInstallationID

	switch {
	case r.Method == http.MethodGet && r.URL.Path == prefix:
		f.lastJWT = bearer

		writeJSON(w, http.StatusOK, map[string]any{
			"id":          9001,
			"account":     map[string]any{"id": 777, "login": fakeLogin},
			"permissions": map[string]string{"metadata": "read", "contents": "write"},
		})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"/access_tokens":
		f.minted++

		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<10)) //nolint:errcheck // A test server.
		f.mintBody = string(body)

		writeJSON(
			w,
			http.StatusCreated,
			map[string]any{"token": "ghs_" + strings.Repeat("x", 36), "expires_at": "2099-01-01T00:00:00Z"},
		)
	case r.Method == http.MethodDelete && r.URL.Path == "/installation/token":
		f.revoked++

		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/user":
		f.lastToken = bearer

		if !f.expiry.IsZero() {
			w.Header().Set("Github-Authentication-Token-Expiration", f.expiry.UTC().Format("2006-01-02 15:04:05 UTC"))
		}

		w.Header().Set("X-Oauth-Scopes", "repo, read:org")
		writeJSON(w, http.StatusOK, map[string]any{"id": 777, "login": fakeLogin})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// authorized checks the credential: the user token for /user, and a valid
// RS256 JWT of the app for the app calls.
func (f *fake) authorized(r *http.Request, bearer string) bool {
	if r.URL.Path == "/user" {
		return bearer == f.token
	}

	if r.URL.Path == "/installation/token" {
		return bearer == "ghs_"+strings.Repeat("x", 36)
	}

	return verifyJWT(bearer, f.pub) == nil
}
