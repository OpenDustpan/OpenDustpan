package githubfake_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAuthentication(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	good := jwt(t, f.PrivateKey(), f.AppID(), 9*time.Minute)

	tests := []struct {
		name   string
		path   string
		bearer string
		want   int
	}{
		{"a JWT of the app reads the installation", "/app/installations/9001", good, http.StatusOK},
		{"no credential is refused", "/app/installations/9001", "", http.StatusUnauthorized},
		{
			"a JWT of another key is refused", "/app/installations/9001",
			jwt(t, otherTestKey(t), f.AppID(), 9*time.Minute), http.StatusUnauthorized,
		},
		{
			"a JWT of another app is refused", "/app/installations/9001",
			jwt(t, f.PrivateKey(), "1", 9*time.Minute), http.StatusUnauthorized,
		},
		{
			"a JWT with a lifetime above ten minutes is refused", "/app/installations/9001",
			jwt(t, f.PrivateKey(), f.AppID(), 11*time.Minute), http.StatusUnauthorized,
		},
		{"an unknown installation is not found", "/app/installations/1", good, http.StatusNotFound},
		{"the user token cannot read the installation", "/app/installations/9001", f.UserToken(), http.StatusForbidden},
		{"the user token reads the user", "/user", f.UserToken(), http.StatusOK},
		{"a JWT cannot read the user", "/user", good, http.StatusForbidden},
		{"a wrong token is refused", "/user", "github_pat_other", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wantStatus(t, c.do(http.MethodGet, tc.path, tc.bearer, ""), tc.want)
		})
	}
}

func TestInstallationTokenLifecycle(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	token := installationToken(t, c, `{"permissions":{"metadata":"read"}}`)
	if !strings.HasPrefix(token, "ghs_") || len(token) != 40 {
		t.Fatalf("token has the length %d, want ghs_ and 36 characters", len(token))
	}

	wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/commits/main", token, ""), http.StatusForbidden)
	wantStatus(t, c.do(http.MethodDelete, "/installation/token", token, ""), http.StatusNoContent)
	wantStatus(t, c.do(http.MethodDelete, "/installation/token", token, ""), http.StatusUnauthorized)
}

func TestInstallationTokenPermissionAboveInstallation(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	a := newClient(t, f).do(http.MethodPost, "/app/installations/9001/access_tokens",
		jwt(t, f.PrivateKey(), f.AppID(), 9*time.Minute), `{"permissions":{"administration":"write"}}`)

	wantStatus(t, a, http.StatusUnprocessableEntity)
}

func TestInstallationTokenOfOneRepository(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	if _, err := f.AddRepo("acme/other", map[string][]byte{"a": []byte("a")}); err != nil {
		t.Fatal(err)
	}

	scoped := installationToken(t, c, `{"repositories":["infra"]}`)

	wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/commits/main", scoped, ""), http.StatusOK)
	wantStatus(t, c.do(http.MethodGet, "/repos/acme/other/commits/main", scoped, ""), http.StatusNotFound)
}

func TestUserExpiryHeader(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	const header = "Github-Authentication-Token-Expiration"

	if got := c.do(http.MethodGet, "/user", f.UserToken(), "").header.Get(header); got != "" {
		t.Fatalf("expiry header = %q, want none", got)
	}

	f.SetTokenExpiry(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC))

	if got := c.do(http.MethodGet, "/user", f.UserToken(), "").header.Get(header); got != "2027-01-02 03:04:05 UTC" {
		t.Fatalf("expiry header = %q", got)
	}
}

func TestInstallationTokenOfMissingRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	a := newClient(t, f).do(http.MethodPost, "/app/installations/9001/access_tokens",
		jwt(t, f.PrivateKey(), f.AppID(), 9*time.Minute), `{"repositories":["none"]}`)

	wantStatus(t, a, http.StatusUnprocessableEntity)

	if got := f.IssuedTokens(); len(got) != 0 {
		t.Fatalf("the fake made tokens: %v", got)
	}
}

func TestInstallationTokenAlwaysReadsMetadata(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	token := installationToken(t, c, `{"repositories":["infra"],"permissions":{"contents":"read"}}`)

	wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/commits/main", token, ""), http.StatusOK)

	if got := f.IssuedTokens(); len(got) != 1 || got[0] != token {
		t.Fatalf("issued tokens = %v, want the one token", got)
	}
}
