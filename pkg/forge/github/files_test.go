package github_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// opened is a fake host with the forge of an adapter that is open on it.
type opened struct {
	host *githubfake.Fake
	fg   forge.Forge
}

// openHost opens the adapter on a fake host with auth.
func openHost(t *testing.T, auth forge.AuthKind) opened {
	t.Helper()

	host := newHost(t)
	conn, cred := hostConnection(t, host, auth)

	fg, err := newHostOpener(t, host).Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	return opened{host: host, fg: fg}
}

func headOf(t *testing.T, host *githubfake.Fake) githubfake.Commit {
	t.Helper()

	c, err := host.Head(conformanceRepo, githubfake.DefaultBranch)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// hostilePaths are paths that leave the repository, or that are not clean, or
// that hold a character that is not a graphic character.
func hostilePaths() []string {
	return []string{
		"", ".", "..", "../README.md", "a/../../README.md", "a/..", "/README.md", ".git/config", "a/.GIT/b",
		".Git", `a\..\b`, "a//b", "a/./b", "a/", "a\x00b", "a\nb", "a" + string(rune(0x202e)) + "b", "a\tb",
		strings.Repeat("a/", 3000),
	}
}

// TestReadFileRefusesHostilePaths sends hostile paths straight to ReadFile.
// Each one fails with ErrInvalid before any request.
func TestReadFileRefusesHostilePaths(t *testing.T) {
	t.Parallel()

	for _, auth := range []forge.AuthKind{forge.AuthToken, forge.AuthApp} {
		o := openHost(t, auth)
		c := headOf(t, o.host)

		for _, p := range hostilePaths() {
			err := readFile(t, o.fg, c.ID, p, 1<<20)
			if !errors.Is(err, forge.ErrInvalid) {
				t.Errorf("%s: ReadFile of %q = %v, want ErrInvalid", auth, p, err)
			}
		}

		if n := o.host.Calls(); n != 0 {
			t.Errorf("%s: hostile paths made %d requests, want none", auth, n)
		}
	}
}

// TestPercentInANameIsLiteral checks that the adapter escapes a percent
// sign, so that "%2e%2e" is a file name and never a part of the path that
// leaves the repository.
func TestPercentInANameIsLiteral(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	c := headOf(t, o.host)

	err := readFile(t, o.fg, c.ID, "%2e%2e/README.md", 1<<20)
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("ReadFile = %v, want ErrNotFound", err)
	}

	reqs := o.host.Requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].URI, "%252e%252e/README.md") {
		t.Fatalf("requests = %+v, want one with the percent sign escaped", reqs)
	}
}

// TestNamesAreEscapedInTheRequest checks the second layer: a file name with
// a character that has a meaning in a URL goes into the path as one part, and
// never changes the query or the path.
func TestNamesAreEscapedInTheRequest(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	host, fg := o.host, o.fg

	names := []string{"a?ref=x.txt", "a#b.txt", "a b.txt", "100%.txt", "dir/a?b/c.txt", "über.txt"}
	files := map[string][]byte{}

	for _, n := range names {
		files[n] = []byte(n)
	}

	c, err := host.Push(conformanceRepo, "main", files, "Add names")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range names {
		file, readErr := fg.ReadFile(t.Context(), conformanceRepo, c.ID, n, 1<<20)
		if readErr != nil || string(file.Content) != n || file.Path != n {
			t.Errorf("ReadFile of %q = %+v, %v", n, file, readErr)
		}
	}

	for _, r := range host.Requests() {
		if strings.Count(r.URI, "?") != 1 || strings.Count(r.URI, "#") != 0 || strings.Contains(r.URI, " ") {
			t.Errorf("a request has the target %q: a name changed the query or the path", r.URI)
		}

		if !strings.HasSuffix(r.URI, "?ref="+c.ID) {
			t.Errorf("the query of %q is not the commit only", r.URI)
		}
	}
}

// TestHeadRefusesHostileNames sends hostile repository paths and refs
// straight to Head. Each one fails with ErrInvalid before any request.
func TestHeadRefusesHostileNames(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	host, fg := o.host, o.fg

	type input struct{ repo, ref string }

	hostile := []input{
		{"", "main"},
		{"acme", "main"},
		{"acme/", "main"},
		{"/infra", "main"},
		{"acme/infra/x", "main"},
		{"../infra", "main"},
		{"acme/..", "main"},
		{"acme/infra?x=1", "main"},
		{"acme/in fra", "main"},
		{"acme/infra#x", "main"},
		{"acme/infra\n", "main"},
		{"acme%2Finfra", "main"},
		{conformanceRepo, ""},
		{conformanceRepo, "../main"},
		{conformanceRepo, "a..b"},
		{conformanceRepo, "main?x=1"},
		{conformanceRepo, "main#x"},
		{conformanceRepo, "a b"},
		{conformanceRepo, "refs/heads/main"},
		{conformanceRepo, "-main"},
		{conformanceRepo, ".main"},
		{conformanceRepo, "main.lock"},
		{conformanceRepo, "a//b"},
		{conformanceRepo, "main\n"},
		{conformanceRepo, "a%2Fb"},
		{conformanceRepo, strings.Repeat("a", 300)},
	}

	for _, in := range hostile {
		if _, err := fg.Head(t.Context(), in.repo, in.ref); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Head(%q, %q) = %v, want ErrInvalid", in.repo, in.ref, err)
		}
	}

	if n := host.Calls(); n != 0 {
		t.Fatalf("hostile names made %d requests, want none", n)
	}
}

// TestReadFileRefusesBadLimit checks that a limit of zero or less is invalid.
func TestReadFileRefusesBadLimit(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	host, fg := o.host, o.fg
	c := headOf(t, host)

	for _, limit := range []int64{0, -1} {
		err := readFile(t, fg, c.ID, "README.md", limit)
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("ReadFile with the limit %d = %v, want ErrInvalid", limit, err)
		}
	}

	if host.Calls() != 0 {
		t.Fatal("a bad limit made a request")
	}
}

// TestSizeLimitStopsBeforeTheContent checks the first layer of the size
// limit: the size that GitHub gives for the file is above the limit, so the
// adapter reads no blob.
func TestSizeLimitStopsBeforeTheContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		size  int
		limit int64
	}{
		{"above the caller limit", 1<<20 + 1, 1 << 20},
		{"above the cap of the adapter", github.MaxFileBytes + 1, 8 << 20},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := openHost(t, forge.AuthToken)
			host, fg := o.host, o.fg
			c, err := host.Push(
				conformanceRepo,
				"main",
				map[string][]byte{"big.bin": bytes.Repeat([]byte("x"), tc.size)},
				"Big",
			)
			if err != nil {
				t.Fatal(err)
			}

			err = readFile(t, fg, c.ID, "big.bin", tc.limit)
			if !errors.Is(err, forge.ErrTooLarge) {
				t.Fatalf("ReadFile = %v, want ErrTooLarge", err)
			}

			for _, r := range host.Requests() {
				if strings.Contains(r.URI, "/git/blobs/") {
					t.Fatalf("the adapter read the blob of a file above the limit: %s", r.URI)
				}
			}
		})
	}
}

// TestReadFileReadsTheBlobOfALargeFile checks a file above 1 MiB that fits
// the limit: the adapter reads the blob, and the content is whole.
func TestReadFileReadsTheBlobOfALargeFile(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	host, fg := o.host, o.fg
	large := bytes.Repeat([]byte("0123456789abcdef"), 1<<16+1)

	c, err := host.Push(conformanceRepo, "main", map[string][]byte{"large.bin": large}, "Large")
	if err != nil {
		t.Fatal(err)
	}

	file, err := fg.ReadFile(t.Context(), conformanceRepo, c.ID, "large.bin", github.MaxFileBytes)
	if err != nil || !bytes.Equal(file.Content, large) || file.BlobID != githubfake.BlobID(large) {
		t.Fatalf("ReadFile = %d bytes, blob %q, %v; want %d bytes", len(file.Content), file.BlobID, err, len(large))
	}
}

// rawFile starts a TLS server that answers the contents call with body, as a
// GitHub that lies, and returns a forge for a token connection to it.
func rawFile(t *testing.T, body []byte) forgeBox {
	t.Helper()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(body); err != nil {
			return
		}
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	o, err := github.NewOpener(github.Config{
		Rules: loopbackRules(t), RootCAs: pool, Now: nil, Prover: testProver{}, AppBudget: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	conn := forge.Connection{
		ID: "c", Kind: forge.KindGitHub, Auth: forge.AuthToken, BaseURL: srv.URL, Params: nil, Push: false,
	}

	fg, err := o.Open(
		t.Context(),
		conn,
		forge.Credential{SetID: "s", Secret: []byte("github_pat_x"), CreatedAt: time.Time{}},
	)
	if err != nil {
		t.Fatal(err)
	}

	return forgeBox{fg: fg}
}

// forgeBox holds a forge.
type forgeBox struct{ fg forge.Forge }

func contentsBody(t *testing.T, path string, size int, content []byte) []byte {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"type": "file", "path": path, "sha": strings.Repeat("a", 40), "size": size, "encoding": "base64",
		"content": base64.StdEncoding.EncodeToString(content),
	})
	if err != nil {
		t.Fatal(err)
	}

	return body
}

// TestSizeLimitChecksTheContentToo sends answers of a GitHub that lies about
// the size. Each one is refused on its own: the content is above the limit,
// the size and the content differ, or the body is above the body limit.
func TestSizeLimitChecksTheContentToo(t *testing.T) {
	t.Parallel()

	commit := strings.Repeat("c", 40)

	tests := []struct {
		name string
		body []byte
		want error
	}{
		{
			"content above the limit with a small size",
			contentsBody(t, "a.txt", 1, bytes.Repeat([]byte("x"), 100)),
			forge.ErrTooLarge,
		},
		{"size above the limit", contentsBody(t, "a.txt", 100, bytes.Repeat([]byte("x"), 100)), forge.ErrTooLarge},
		{
			"size and content differ",
			contentsBody(t, "a.txt", 10, bytes.Repeat([]byte("x"), 20)[:5]),
			forge.ErrUnavailable,
		},
		{"a negative size", contentsBody(t, "a.txt", -1, []byte("x")), forge.ErrTooLarge},
		{"another path", contentsBody(t, "b.txt", 1, []byte("x")), forge.ErrUnavailable},
		{"a body above the body limit", bytes.Repeat([]byte("x"), 5<<20), forge.ErrTooLarge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := rawFile(t, tc.body).fg.ReadFile(t.Context(), conformanceRepo, commit, "a.txt", 50)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ReadFile = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestReadsRefuseARedirect checks that Head and ReadFile follow no redirect,
// and that the credential never reaches the host that the redirect names.
func TestReadsRefuseARedirect(t *testing.T) {
	t.Parallel()

	for _, auth := range []forge.AuthKind{forge.AuthToken, forge.AuthApp} {
		o := openHost(t, auth)
		host, fg := o.host, o.fg
		other := githubfake.New(t, githubfake.Config{
			AppID:          "",
			AppSlug:        "",
			InstallationID: "",
			Owner:          githubfake.Account{ID: 0, Login: ""},
			ContentsLimit:  0,
			User: githubfake.Account{
				ID:    0,
				Login: "",
			},
			UserToken:     "",
			ClassicToken:  false,
			Permissions:   nil,
			Key:           nil,
			Now:           nil,
			WebhookClient: nil,
		})
		c := headOf(t, host)

		// An app asks for its token before the first read, so the redirect
		// starts after that.
		if auth == forge.AuthApp {
			if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
				t.Fatal(err)
			}
		}

		host.Redirect(other.URL() + "/steal")

		if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err == nil {
			t.Errorf("%s: Head followed a redirect", auth)
		}

		if _, err := fg.ReadFile(t.Context(), conformanceRepo, c.ID, "README.md", 1<<20); err == nil {
			t.Errorf("%s: ReadFile followed a redirect", auth)
		}

		if n := other.Calls(); n != 0 {
			t.Errorf("%s: the host of the redirect got %d requests", auth, n)
		}
	}
}

// TestAppReadsUseAScopedToken checks that an app connection reads with an
// installation token for the one repository, with the read permission of the
// contents only, and keeps the token for the next read.
func TestAppReadsUseAScopedToken(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	host, fg := o.host, o.fg
	c := headOf(t, host)

	for range 3 {
		if _, err := fg.ReadFile(t.Context(), conformanceRepo, c.ID, "README.md", 1<<20); err != nil {
			t.Fatal(err)
		}
	}

	if n := countReads(t, host.Requests()); n != (readCounts{mints: 1, owners: 1, reads: 3}) {
		t.Fatalf("%+v, want 1 token request, 1 installation read and 3 reads", n)
	}
}

// countReads counts the token requests, the installation reads and the reads
// of the requests, and checks the credential of each.
func countReads(t *testing.T, reqs []githubfake.Request) readCounts {
	t.Helper()

	var mints, owners, reads int

	for _, r := range reqs {
		want := githubfake.BearerInstallation

		switch {
		case strings.HasSuffix(r.URI, "/access_tokens"):
			mints++
			want = githubfake.BearerApp

			if string(r.Body) != `{"repositories":["infra"],"permissions":{"contents":"read"}}` {
				t.Errorf("token request = %s", r.Body)
			}
		case strings.HasPrefix(r.URI, "/app/installations/"):
			// The account of the installation, read once, with the key of the app.
			owners++
			want = githubfake.BearerApp
		default:
			reads++
		}

		if r.Bearer != want {
			t.Errorf("%s used the credential %s, want %s", r.URI, r.Bearer, want)
		}
	}

	return readCounts{mints: mints, owners: owners, reads: reads}
}

// readCounts are the counts of countReads.
type readCounts struct{ mints, owners, reads int }

// TestTokenReadsUseTheToken checks that a token connection reads with its
// token.
func TestTokenReadsUseTheToken(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)
	host, fg := o.host, o.fg

	if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
		t.Fatal(err)
	}

	if r := host.Requests(); len(r) != 1 || r[0].Bearer != githubfake.BearerUser {
		t.Fatalf("requests = %+v, want one with the token", r)
	}
}

// TestRefusedReadTokenIsReplaced checks that after GitHub refuses the
// installation token, the next read asks for a new one.
func TestRefusedReadTokenIsReplaced(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	host, fg := o.host, o.fg

	if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
		t.Fatal(err)
	}

	restore := host.Refuse()

	if _, err := fg.Head(t.Context(), conformanceRepo, "main"); !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("Head after the refusal = %v, want ErrAuth", err)
	}

	restore()

	if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
		t.Fatalf("Head after the restore = %v", err)
	}

	if got := len(host.IssuedTokens()); got != 2 {
		t.Fatalf("the host made %d tokens, want 2", got)
	}
}

// TestAppReadOfARepositoryOutsideTheInstallation checks that a repository
// that the installation cannot read is not found.
func TestAppReadOfARepositoryOutsideTheInstallation(t *testing.T) {
	t.Parallel()

	fg := openHost(t, forge.AuthApp).fg

	if _, err := fg.Head(t.Context(), "acme/elsewhere", "main"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Head = %v, want ErrNotFound", err)
	}
}

// TestNoCredentialInAnyError runs each failure of a read and looks for the
// credentials in the text of the error. The adapter takes no logger and
// writes to no log, so the errors are the only text that could hold one.
func TestNoCredentialInAnyError(t *testing.T) {
	t.Parallel()

	for _, auth := range []forge.AuthKind{forge.AuthToken, forge.AuthApp} {
		o := openHost(t, auth)
		host, fg := o.host, o.fg
		c := headOf(t, host)

		failures := map[string]func() func(){
			"refused":     func() func() { return host.Refuse() },
			"garbled":     func() func() { return host.Garble() },
			"unavailable": func() func() { return host.Unavailable(0) },
			"rate limit":  func() func() { return host.Unavailable(time.Second) },
			"redirect":    func() func() { return host.Redirect("https://169.254.169.254/x") },
			"status":      func() func() { return host.Respond(http.StatusTeapot, nil) },
			"none":        func() func() { return func() {} },
		}

		// Prime the token of an app, so that its value exists to look for.
		if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
			t.Fatal(err)
		}

		secrets := append([]string{host.UserToken(), string(pkcs1PEM(host.PrivateKey()))}, host.IssuedTokens()...)

		for name, inject := range failures {
			restore := inject()

			errs := readErrors(t.Context(), fg, c.ID)
			restore()

			if err := errorWithAny(errs, secrets); err != nil {
				t.Errorf("%s, %s: an error holds a credential: %v", auth, name, err)
			}
		}
	}
}

// readErrors makes the failing and the missing reads and returns the errors.
func readErrors(ctx context.Context, fg forge.Forge, commit string) []error {
	_, headErr := fg.Head(ctx, conformanceRepo, "main")
	_, missingHeadErr := fg.Head(ctx, conformanceRepo, "no-such-ref")
	_, readErr := fg.ReadFile(ctx, conformanceRepo, commit, "README.md", 1<<20)
	_, missingErr := fg.ReadFile(ctx, conformanceRepo, commit, "none.txt", 1<<20)
	_, largeErr := fg.ReadFile(ctx, conformanceRepo, commit, "README.md", 1)
	_, badErr := fg.ReadFile(ctx, conformanceRepo, commit, "../x", 1<<20)

	return []error{headErr, missingHeadErr, readErr, missingErr, largeErr, badErr}
}

// TestReadTokenExpires checks that the adapter asks for a new installation
// token when the one it has is about to expire, and not before.
func TestReadTokenExpires(t *testing.T) {
	t.Parallel()

	host := newHost(t)
	conn, cred := hostConnection(t, host, forge.AuthApp)

	var offset atomic.Int64

	now := func() time.Time { return githubfake.Epoch().Add(time.Duration(offset.Load())) }

	fg, err := newHostOpenerAt(t, host, now).Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	read := func() {
		t.Helper()

		if _, err := fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
			t.Fatal(err)
		}
	}

	read()
	offset.Store(int64(30 * time.Minute))
	read()

	if got := len(host.IssuedTokens()); got != 1 {
		t.Fatalf("after 30 minutes the host made %d tokens, want 1", got)
	}

	// The token lives one hour from the time that the host made it, and the
	// adapter stops to use it one minute before.
	offset.Store(int64(61 * time.Minute))
	read()

	if got := len(host.IssuedTokens()); got != 2 {
		t.Fatalf("after 61 minutes the host made %d tokens, want 2", got)
	}
}

// readFile reads a file of the repository of the suite and returns the error.
func readFile(t *testing.T, fg forge.Forge, commit, p string, limit int64) error {
	t.Helper()

	_, err := fg.ReadFile(t.Context(), conformanceRepo, commit, p, limit)

	return err
}

// errorWithAny returns the first error whose text holds one of the secrets.
func errorWithAny(errs []error, secrets []string) error {
	for _, err := range errs {
		for _, s := range secrets {
			if err != nil && strings.Contains(err.Error(), s) {
				return err
			}
		}
	}

	return nil
}
