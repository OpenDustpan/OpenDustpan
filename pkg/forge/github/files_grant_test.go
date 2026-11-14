package github_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

// openAppWith opens an app connection on a host with the installation
// permissions that the test gives.
func openAppWith(t *testing.T, permissions map[string]string) opened {
	t.Helper()

	host := newHostWith(t, func(c *githubfake.Config) { c.Permissions = permissions })
	conn, cred := hostConnection(t, host, forge.AuthApp)

	fg, err := newHostOpener(t, host).Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	return opened{host: host, fg: fg}
}

// TestFilesCapabilityFollowsThePermissions checks that an installation with no
// permission to read the contents lists no files capability after a check,
// so that Self-service shows the feature as missing.
func TestFilesCapabilityFollowsThePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		permissions map[string]string
		want        bool
	}{
		{"contents read", map[string]string{"contents": "read", "metadata": "read"}, true},
		{"contents write", map[string]string{"contents": "write", "metadata": "read"}, true},
		{"metadata only", map[string]string{"metadata": "read"}, false},
		{"another permission", map[string]string{"issues": "write", "metadata": "read"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := openAppWith(t, tc.permissions)

			// Before a check, the permissions are not known.
			if !o.fg.Capabilities().Has(forge.CapFiles) {
				t.Fatal("the capability is not listed before the first check")
			}

			if _, err := o.fg.Check(t.Context()); err != nil {
				t.Fatalf("Check = %v", err)
			}

			if got := o.fg.Capabilities().Has(forge.CapFiles); got != tc.want {
				t.Fatalf("after Check, files listed = %v, want %v", got, tc.want)
			}

			missing := o.fg.Capabilities().Missing(forge.Needs(forge.ConsumerSelfService))
			if (len(missing) == 0) != tc.want {
				t.Fatalf("Self-service misses %v, want files missing = %v", missing, !tc.want)
			}
		})
	}
}

// TestReadWithoutThePermissionIsAuth checks that the read of an installation
// that lacks the permission is a credential error, and not a missing file.
func TestReadWithoutThePermissionIsAuth(t *testing.T) {
	t.Parallel()

	o := openAppWith(t, map[string]string{"metadata": "read"})

	if _, err := o.fg.Head(t.Context(), conformanceRepo, "main"); !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("Head = %v, want ErrAuth", err)
	}

	err := readFile(t, o.fg, strings.Repeat("a", 40), "README.md", 1<<20)
	if !errors.Is(err, forge.ErrAuth) || errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("ReadFile = %v, want ErrAuth", err)
	}
}

// TestTokenConnectionAlwaysListsFiles checks that a token shows no
// permissions, so the capability does not depend on a check.
func TestTokenConnectionAlwaysListsFiles(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	if _, err := o.fg.Check(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !o.fg.Capabilities().Has(forge.CapFiles) {
		t.Fatal("a token connection lists no files capability")
	}
}

// rawApp starts a TLS server that plays the part of GitHub for an app
// connection: it answers the installation with the account acme and the
// permission to read the contents, and a request for a token with tokenBody.
// It counts the requests that follow a token, and returns a forge for it.
func rawApp(t *testing.T, tokenBody string) rawHost {
	t.Helper()

	var reads atomic.Int64

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var body string

		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/app/installations/"):
			body = `{"account":{"id":99,"login":"acme"},"permissions":{"contents":"read","metadata":"read"}}`
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)

			body = tokenBody
		default:
			reads.Add(1)
			w.WriteHeader(http.StatusNotFound)

			body = `{}`
		}

		if _, err := w.Write([]byte(body)); err != nil {
			return
		}
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	o, err := github.NewOpener(
		github.Config{Rules: loopbackRules(t), RootCAs: pool, Now: nil, Prover: testProver{}, AppBudget: nil},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	conn := forge.Connection{
		ID: "c", Kind: forge.KindGitHub, Auth: forge.AuthApp, BaseURL: srv.URL, Push: false,
		Params: map[string]string{forge.ParamAppID: "4242", forge.ParamInstallationID: "9001"},
	}

	fg, err := o.Open(t.Context(), conn, forge.Credential{
		SetID: "s", Secret: pkcs1PEM(testKey()), CreatedAt: time.Time{},
	})
	if err != nil {
		t.Fatal(err)
	}

	return rawHost{fg: fg, reads: &reads}
}

// rawHost is a forge for the server of rawApp, with the count of its reads.
type rawHost struct {
	fg    forge.Forge
	reads *atomic.Int64
}

// TestWiderTokenIsRefused sends answers to the token request that give more
// than the adapter asked for. Each one is refused, and the adapter sends no
// read with the token.
func TestWiderTokenIsRefused(t *testing.T) {
	t.Parallel()

	const answerStart = `"token":"ghs_wider"`

	tests := []struct {
		name string
		body string
	}{
		{
			"contents write",
			`{` + answerStart + `,"permissions":{"contents":"write","metadata":"read"},"repository_selection":"selected","repositories":[{"name":"infra"}]}`,
		},
		{
			"another permission",
			`{` + answerStart + `,"permissions":{"contents":"read","administration":"read"},"repository_selection":"selected","repositories":[{"name":"infra"}]}`,
		},
		{"all repositories", `{` + answerStart + `,"permissions":{"contents":"read"},"repository_selection":"all"}`},
		{
			"two repositories",
			`{` + answerStart + `,"permissions":{"contents":"read"},"repository_selection":"selected","repositories":[{"name":"infra"},{"name":"other"}]}`,
		},
		{
			"another repository",
			`{` + answerStart + `,"permissions":{"contents":"read"},"repository_selection":"selected","repositories":[{"name":"other"}]}`,
		},
		{
			"no repository list",
			`{` + answerStart + `,"permissions":{"contents":"read"},"repository_selection":"selected"}`,
		},
		{
			"all repositories with a list",
			`{` + answerStart + `,"permissions":{"contents":"read"},"repository_selection":"all","repositories":[{"name":"infra"}]}`,
		},
		{"no selection", `{` + answerStart + `,"permissions":{"contents":"read"},"repositories":[{"name":"infra"}]}`},
		{"no permissions", `{` + answerStart + `,"repository_selection":"selected","repositories":[{"name":"infra"}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := rawApp(t, tc.body)

			_, err := raw.fg.Head(t.Context(), conformanceRepo, "main")
			if !errors.Is(err, forge.ErrUnavailable) {
				t.Fatalf("Head = %v, want ErrUnavailable", err)
			}

			if raw.reads.Load() != 0 {
				t.Fatal("the adapter used a token that is wider than the request")
			}

			if strings.Contains(err.Error(), "ghs_wider") {
				t.Fatalf("the error holds the token: %v", err)
			}
		})
	}
}

// TestNarrowTokenIsUsed is the control of the test above: a token that is as
// wide as the request passes the check.
func TestNarrowTokenIsUsed(t *testing.T) {
	t.Parallel()

	raw := rawApp(t, `{"token":"ghs_ok","permissions":{"contents":"read","metadata":"read"},`+
		`"repository_selection":"selected","repositories":[{"name":"infra"}]}`)

	if _, err := raw.fg.Head(t.Context(), conformanceRepo, "main"); !errors.Is(err, forge.ErrUnavailable) &&
		!errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Head = %v", err)
	}

	if raw.reads.Load() != 1 {
		t.Fatalf("the adapter sent %d reads with the token, want 1", raw.reads.Load())
	}
}

// TestAppRefusesAnotherAccount checks that an app connection reads no
// repository of an account other than the one of its installation, and asks
// for no token for it.
func TestAppRefusesAnotherAccount(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)

	if _, err := o.host.AddRepo("stranger/infra", map[string][]byte{"a": []byte("a")}); err != nil {
		t.Fatal(err)
	}

	before := o.host.Calls()

	if _, err := o.fg.Head(t.Context(), "stranger/infra", "main"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Head = %v, want ErrNotFound", err)
	}

	_, err := o.fg.ReadFile(t.Context(), "stranger/infra", strings.Repeat("a", 40), "a", 1<<20)
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("ReadFile = %v, want ErrNotFound", err)
	}

	for _, r := range o.host.Requests()[before:] {
		if strings.HasSuffix(r.URI, "/access_tokens") || strings.Contains(r.URI, "stranger") {
			t.Errorf("the adapter sent %s %s for another account", r.Method, r.URI)
		}
	}
}

// TestReadTokenCacheIsBounded checks that the adapter keeps at most
// MaxReadTokens tokens: the token that expires first goes, and the newest
// stays.
func TestReadTokenCacheIsBounded(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	total := github.MaxReadTokens + 10

	for i := range total {
		name := fmt.Sprintf("repo-%03d", i)
		if _, err := o.host.AddRepo("acme/"+name, map[string][]byte{"a": []byte("a")}); err != nil {
			t.Fatal(err)
		}

		if _, err := o.fg.Head(t.Context(), "acme/"+name, "main"); err != nil {
			t.Fatal(err)
		}
	}

	if got := len(o.host.IssuedTokens()); got != total {
		t.Fatalf("the host made %d tokens, want %d", got, total)
	}

	// The newest token is in the cache.
	if _, err := o.fg.Head(t.Context(), fmt.Sprintf("acme/repo-%03d", total-1), "main"); err != nil {
		t.Fatal(err)
	}

	if got := len(o.host.IssuedTokens()); got != total {
		t.Fatalf("the newest token was not kept: %d tokens", got)
	}

	// The oldest token went. A new one is made for it.
	if _, err := o.fg.Head(t.Context(), "acme/repo-000", "main"); err != nil {
		t.Fatal(err)
	}

	if got := len(o.host.IssuedTokens()); got != total+1 {
		t.Fatalf("the oldest token was kept: %d tokens, want %d", got, total+1)
	}
}

// TestFileThatTheAPIDoesNotServeIsTooLarge checks that GitHub's 403 with the
// error code too_large is the size error.
func TestFileThatTheAPIDoesNotServeIsTooLarge(t *testing.T) {
	t.Parallel()

	host := newHostWith(t, func(c *githubfake.Config) { c.ContentsLimit = 100 })
	conn, cred := hostConnection(t, host, forge.AuthToken)

	fg, err := newHostOpener(t, host).Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	c, err := host.Push(
		conformanceRepo,
		"main",
		map[string][]byte{"large.bin": bytes.Repeat([]byte("x"), 101)},
		"Large",
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := readFile(t, fg, c.ID, "large.bin", 1<<20); !errors.Is(err, forge.ErrTooLarge) {
		t.Fatalf("ReadFile = %v, want ErrTooLarge", err)
	}
}

// TestRenamedRepositoryIsNotFoundAndNotFollowed checks that a repository with
// a new name is a clear not-found error that names the move, and that the
// adapter does not follow the redirect.
func TestRenamedRepositoryIsNotFoundAndNotFollowed(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	c := headOf(t, o.host)

	if err := o.host.RenameRepo(conformanceRepo, "acme/platform"); err != nil {
		t.Fatal(err)
	}

	_, headErr := o.fg.Head(t.Context(), conformanceRepo, "main")
	readErr := readFile(t, o.fg, c.ID, "README.md", 1<<20)

	for name, err := range map[string]error{"Head": headErr, "ReadFile": readErr} {
		if !errors.Is(err, forge.ErrNotFound) || !strings.Contains(err.Error(), "moved") {
			t.Errorf("%s = %v, want ErrNotFound that names the move", name, err)
		}
	}

	for _, r := range o.host.Requests() {
		if strings.Contains(r.URI, "platform") {
			t.Errorf("the adapter followed the redirect to %s", r.URI)
		}
	}
}

// TestOwnerCompareIgnoresCase checks that GitHub logins are case-insensitive:
// an owner that differs only in case is accepted, and any other owner is
// refused before a token request or a read.
func TestOwnerCompareIgnoresCase(t *testing.T) {
	t.Parallel()

	const answer = `{"token":"ghs_ok","permissions":{"contents":"read","metadata":"read"},` +
		`"repository_selection":"selected","repositories":[{"name":"infra"}]}`

	tests := []struct {
		repo  string
		reads int64
		found bool
	}{
		{"acme/infra", 1, false},
		{"ACME/infra", 1, false},
		{"Acme/infra", 1, false},
		{"acme-org/infra", 0, true},
		{"acm/infra", 0, true},
		{"other/infra", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.repo, func(t *testing.T) {
			t.Parallel()

			raw := rawApp(t, answer)
			_, err := raw.fg.Head(t.Context(), tc.repo, "main")

			// The server of the test answers 404 to a read, so an accepted
			// owner is a read that reached it.
			if raw.reads.Load() != tc.reads {
				t.Fatalf("%d reads reached the server, want %d (error %v)", raw.reads.Load(), tc.reads, err)
			}

			refused := errors.Is(err, forge.ErrNotFound) && strings.Contains(err.Error(), "account")
			if refused != tc.found {
				t.Fatalf("Head = %v, refused for the account = %v, want %v", err, refused, tc.found)
			}
		})
	}
}
