package githubfake_test

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
)

// userValue is the token of the user of the fake.
const userValue = "github_pat_fake"

// answer is the result of one call to the fake.
type answer struct {
	status int
	header http.Header
	body   []byte
}

// client calls a fake over TLS.
type client struct {
	t    *testing.T
	f    *githubfake.Fake
	http *http.Client
}

func newClient(t *testing.T, f *githubfake.Fake) *client {
	t.Helper()

	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.CertPool(), MinVersion: tls.VersionTLS12}}
	t.Cleanup(tr.CloseIdleConnections)

	return &client{
		t: t, f: f,
		http: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// do sends a request with the bearer credential, if it is not empty.
func (c *client) do(method, path, bearer, body string) answer {
	c.t.Helper()

	req, err := http.NewRequestWithContext(c.t.Context(), method, c.f.URL()+path, bytes.NewReader([]byte(body)))
	if err != nil {
		c.t.Fatal(err)
	}

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if err := errors.Join(readErr, resp.Body.Close()); err != nil {
		c.t.Fatal(err)
	}

	return answer{status: resp.StatusCode, header: resp.Header, body: data}
}

// jwt returns the JWT of the app appID signed with key.
func jwt(t *testing.T, key *rsa.PrivateKey, appID string, lifetime time.Duration) string {
	t.Helper()

	now := time.Now()

	claims, err := json.Marshal(map[string]any{
		"iss": appID, "iat": now.Unix(), "exp": now.Add(lifetime).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	enc := base64.RawURLEncoding
	signing := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}

	return signing + "." + enc.EncodeToString(sig)
}

// newFake starts a fake with one repository.
func newFake(t *testing.T) (*githubfake.Fake, githubfake.Commit) {
	t.Helper()

	return newFakeWith(t, nil, false)
}

// newFakeWith starts a fake with the permissions of the installation, and a
// user token that is a classic token when classic is true.
func newFakeWith(t *testing.T, permissions map[string]string, classic bool) (*githubfake.Fake, githubfake.Commit) {
	t.Helper()

	f := githubfake.New(t, githubfake.Config{
		AppID: "", InstallationID: "", Owner: githubfake.Account{ID: 0, Login: ""},
		User: githubfake.Account{ID: 0, Login: ""}, UserToken: userValue, ClassicToken: classic,
		Permissions: permissions, Key: nil, Now: nil, WebhookClient: nil,
	})

	c, err := f.AddRepo("acme/infra", map[string][]byte{
		"README.md":         []byte("hello\n"),
		"stacks/prod.yaml":  []byte("config: prod\n"),
		"stacks/stage.yaml": []byte("config: stage\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	return f, c
}

// installationToken makes an installation token with the optional body.
func installationToken(t *testing.T, c *client, body string) string {
	t.Helper()

	a := c.do(http.MethodPost, "/app/installations/"+c.f.InstallationID()+"/access_tokens",
		jwt(t, c.f.PrivateKey(), c.f.AppID(), 9*time.Minute), body)
	if a.status != http.StatusCreated {
		t.Fatalf("make a token: status %d, body %s", a.status, a.body)
	}

	var out struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(a.body, &out); err != nil {
		t.Fatal(err)
	}

	return out.Token
}

// otherTestKey returns a key that is not the key of the fake.
func otherTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	return key
}

// decode reads one JSON value of type T from body.
func decode[T any](t *testing.T, body []byte) T {
	t.Helper()

	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return v
}

// mustPush pushes files and fails the test on an error.
func mustPush(t *testing.T, f *githubfake.Fake, branch string, files map[string][]byte) githubfake.Commit {
	t.Helper()

	c, err := f.Push("acme/infra", branch, files, "Push to "+branch)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// wantStatus fails the test when the status of a is not want.
func wantStatus(t *testing.T, a answer, want int) {
	t.Helper()

	if a.status != want {
		t.Fatalf("status = %d, want %d (body %s)", a.status, want, a.body)
	}
}
