package githubfake_test

import (
	"encoding/json"
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

func TestUnknownPathAndWrongMethodAreJSONNotFound(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"an unknown path", http.MethodGet, "/nothing/here"},
		{"the root", http.MethodGet, "/"},
		{"a wrong method on the user", http.MethodPost, "/user"},
		{"a wrong method on the installation", http.MethodDelete, "/app/installations/9001"},
		{"a wrong method on the contents", http.MethodPut, "/repos/acme/infra/contents/README.md"},
		{"a wrong method on the graphql path", http.MethodGet, "/graphql"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := c.do(tc.method, tc.path, f.UserToken(), "")
			wantStatus(t, a, http.StatusNotFound)

			if got := decode[map[string]string](t, a.body)["message"]; got != "Not Found" {
				t.Fatalf("message = %q, body %s", got, a.body)
			}

			if !strings.HasPrefix(a.header.Get("Content-Type"), "application/json") {
				t.Fatalf("Content-Type = %q", a.header.Get("Content-Type"))
			}
		})
	}
}

func TestScopesHeaderOnlyForAClassicToken(t *testing.T) {
	t.Parallel()

	for _, classic := range []bool{false, true} {
		f, _ := newFakeWith(t, nil, classic)
		got := newClient(t, f).do(http.MethodGet, "/user", f.UserToken(), "").header.Get("X-Oauth-Scopes")

		if (got != "") != classic {
			t.Errorf("classic %v: X-OAuth-Scopes = %q", classic, got)
		}
	}
}

func TestInstallationPermissionsAreReadOnlyByDefault(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)
	token := installationToken(t, c, "")

	out := decode[gqlOut](t, c.do(http.MethodPost, "/graphql", token, mutation(t, "main", first.ID, "x", "x")).body)
	if len(out.Errors) != 1 || out.Errors[0].Type != "FORBIDDEN" {
		t.Fatalf("a commit with the default permissions: %+v", out)
	}

	a := c.do(http.MethodGet, "/app/installations/9001", jwt(t, f.PrivateKey(), f.AppID(), 9*time.Minute), "")
	got := decode[installationAnswer](t, a.body).Permissions

	if len(got) != 2 || got["contents"] != "read" || got["metadata"] != "read" {
		t.Fatalf("permissions = %v, want contents read and metadata read", got)
	}
}

type installationAnswer struct {
	Permissions map[string]string `json:"permissions"`
}

func TestInstallationWithWriteCanCommit(t *testing.T) {
	t.Parallel()

	f, first := newFakeWith(t, map[string]string{"contents": "write", "metadata": "read"}, false)
	c := newClient(t, f)
	token := installationToken(t, c, "")

	out := decode[gqlOut](t, c.do(http.MethodPost, "/graphql", token, mutation(t, "main", first.ID, "x", "x")).body)
	if out.Data.Create == nil {
		t.Fatalf("a commit with contents write: %+v", out)
	}
}

func TestInstallationTokenReachesTheOwnerOfTheInstallationOnly(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	if _, err := f.AddRepo("stranger/infra", map[string][]byte{"a": []byte("a")}); err != nil {
		t.Fatal(err)
	}

	token := installationToken(t, c, "")

	wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/commits/main", token, ""), http.StatusOK)
	wantStatus(t, c.do(http.MethodGet, "/repos/stranger/infra/commits/main", token, ""), http.StatusNotFound)
	// The user token is not bound to an installation.
	wantStatus(t, c.do(http.MethodGet, "/repos/stranger/infra/commits/main", f.UserToken(), ""), http.StatusOK)
}

func TestTokenAnswerNamesTheSelectedRepositories(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	app := jwt(t, f.PrivateKey(), f.AppID(), 9*time.Minute)

	a := c.do(http.MethodPost, "/app/installations/9001/access_tokens", app, `{"repositories":["infra"]}`)
	fields := decode[map[string]json.RawMessage](t, a.body)

	if string(fields["repository_selection"]) != `"selected"` {
		t.Fatalf("repository_selection = %s", fields["repository_selection"])
	}

	repos := decode[[]map[string]string](t, fields["repositories"])
	if len(repos) != 1 || repos[0]["name"] != "infra" || repos[0]["full_name"] != "acme/infra" {
		t.Fatalf("repositories = %v", repos)
	}

	b := c.do(http.MethodPost, "/app/installations/9001/access_tokens", app, "")
	if string(decode[map[string]json.RawMessage](t, b.body)["repository_selection"]) != `"all"` {
		t.Fatalf("repository_selection of a token for all = %s", b.body)
	}
}
