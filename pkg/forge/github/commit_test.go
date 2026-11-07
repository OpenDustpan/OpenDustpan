package github_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

const (
	rawBase   = "0123456789abcdef0123456789abcdef01234567"
	rawCommit = "89abcdef0123456789abcdef0123456789abcdef"
	// rawIdentity is the login of the user of the token of rawForge.
	rawIdentity = "acme"
)

// rawClient is the forge for the server of rawForge.
type rawClient struct {
	forge.Forge
}

// rawForge starts a TLS server with the handler h, which plays the part of
// GitHub, and returns a forge for a token connection with push access whose
// API address is the server and basePath.
func rawForge(t *testing.T, basePath string, h http.HandlerFunc) rawClient {
	t.Helper()

	return rawForgeAs(t, basePath, http.StatusOK, `{"id":777,"login":"`+rawIdentity+`"}`, h)
}

// rawForgeAs is rawForge with the answer of the call for the user of the token,
// which is the identity that the connection commits as.
func rawForgeAs(t *testing.T, basePath string, userStatus int, userBody string, h http.HandlerFunc) rawClient {
	t.Helper()

	return rawForgeWith(t, basePath, rawOptions{
		userStatus: userStatus, userBody: userBody, repoStatus: http.StatusOK, repoBody: `{"id":` + rawRepoID + `}`,
		prover: testProver{},
	}, h)
}

// rawOptions are the answers of rawForgeWith for the calls that every raw
// server answers by itself, and the Prover of the connection.
type rawOptions struct {
	// userStatus and userBody are the answer for the user of the token.
	userStatus int
	userBody   string
	// repoStatus and repoBody are the answer for the repository, which has the
	// ID that a proof binds.
	repoStatus int
	repoBody   string
	prover     github.Prover
}

// rawForgeWith is rawForgeAs with more options.
func rawForgeWith(t *testing.T, basePath string, opts rawOptions, h http.HandlerFunc) rawClient {
	t.Helper()

	withUser := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == basePath+"/user":
			answer(w, opts.userStatus, opts.userBody)
		case r.Method == http.MethodGet && r.URL.Path == basePath+"/repos/acme/infra":
			answer(w, opts.repoStatus, opts.repoBody)
		default:
			h(w, r)
		}
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(withUser))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	o, err := github.NewOpener(github.Config{Rules: loopbackRules(t), RootCAs: pool, Now: nil, Prover: opts.prover})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	conn := forge.Connection{
		ID: "c", Kind: forge.KindGitHub, Auth: forge.AuthToken, BaseURL: srv.URL + basePath, Push: true, Params: nil,
	}

	fg, err := o.Open(t.Context(), conn, forge.Credential{
		SetID: "s", Secret: []byte("github_pat_" + randomHex(t, 24)), CreatedAt: time.Time{},
	})
	if err != nil {
		t.Fatal(err)
	}

	return rawClient{Forge: fg}
}

// answer writes status and body.
func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}

func oneCommit(base string) forge.CommitRequest {
	return forge.CommitRequest{
		Branch: "main", NewBranch: false, Base: base, Message: "Change\n\nDustpan-Job: j-1",
		Trailer: "Dustpan-Job: j-1",
		Author:  forge.Person{Name: "Ada Lovelace", Email: "ada@example.com"},
		Changes: []forge.FileChange{{Path: "a.txt", Content: []byte("a"), Delete: false}},
	}
}

// fixedAnswer serves status, headers and body for each call, except the read of
// a branch: the head of the branch is the base, so a moved head is a conflict.
func fixedAnswer(status int, headers map[string]string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/ref/heads/") {
			answer(w, http.StatusOK, `{"object":{"sha":"`+rawBase+`"}}`)

			return
		}

		for k, v := range headers {
			w.Header().Set(k, v)
		}

		answer(w, status, body)
	}
}

// typedErrors is an answer with one error for each type, each with the text.
func typedErrors(text string, types ...string) string {
	parts := make([]string, 0, len(types))
	for _, ty := range types {
		parts = append(parts, `{"type":"`+ty+`","message":"`+text+`"}`)
	}

	return `{"data":null,"errors":[` + strings.Join(parts, ",") + `]}`
}

func goodAnswer() string {
	return `{"data":{"createCommitOnBranch":{"commit":{"oid":"` + rawCommit +
		`","committedDate":"2026-01-01T00:00:05Z"}}}}`
}

// TestCommitMapsTheAnswersOfGitHub sends each kind of answer, and checks the
// sentinel of the port. The text of the answer, which may hold anything, is not
// in the error.
func TestCommitMapsTheAnswersOfGitHub(t *testing.T) {
	t.Parallel()

	canary := "CANARY-text-of-the-host"
	errorsOf := func(types ...string) string { return typedErrors(canary, types...) }

	tests := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		want    error
		wait    time.Duration
	}{
		{"a moved head", 200, nil, errorsOf("STALE_DATA"), forge.ErrConflict, 0},
		{"a missing branch", 200, nil, errorsOf("NOT_FOUND"), forge.ErrNotFound, 0},
		{"a rule or a missing permission", 200, nil, errorsOf("FORBIDDEN"), forge.ErrRefused, 0},
		{"a missing scope", 200, nil, errorsOf("INSUFFICIENT_SCOPES"), forge.ErrRefused, 0},
		{"an input that is not valid", 200, nil, errorsOf("UNPROCESSABLE"), forge.ErrInvalid, 0},
		{"a rate limit in the answer", 200, nil, errorsOf("RATE_LIMITED"), forge.ErrUnavailable, 0},
		{"a type that the adapter does not know", 200, nil, errorsOf("NEW_KIND"), forge.ErrUnavailable, 0},
		{"an error with no type", 200, nil, `{"errors":[{"message":"` + canary + `"}]}`, forge.ErrUnavailable, 0},
		{"a moved head before an input error", 200, nil, errorsOf("UNPROCESSABLE", "STALE_DATA"), forge.ErrConflict, 0},
		{
			"an error next to a commit", 200, nil,
			`{"data":{"createCommitOnBranch":{"commit":{"oid":"` + rawCommit +
				`","committedDate":"2026-01-01T00:00:05Z"}}},"errors":[{"type":"FORBIDDEN"}]}`,
			forge.ErrRefused, 0,
		},
		{"no commit in the answer", 200, nil, `{"data":{"createCommitOnBranch":null}}`, forge.ErrUnavailable, 0},
		{"an empty object", 200, nil, `{}`, forge.ErrUnavailable, 0},
		{
			"a commit ID that is not valid", 200, nil,
			`{"data":{"createCommitOnBranch":{"commit":{"oid":"main","committedDate":"2026-01-01T00:00:05Z"}}}}`,
			forge.ErrUnavailable, 0,
		},
		{
			"a commit with no time", 200, nil,
			`{"data":{"createCommitOnBranch":{"commit":{"oid":"` + rawCommit + `"}}}}`, forge.ErrUnavailable, 0,
		},
		{"a body that does not parse", 200, nil, `not json {`, forge.ErrUnavailable, 0},
		{"401", 401, nil, `{"message":"` + canary + `"}`, forge.ErrAuth, 0},
		{"403", 403, nil, `{"message":"` + canary + `"}`, forge.ErrRefused, 0},
		{"404", 404, nil, `{"message":"` + canary + `"}`, forge.ErrNotFound, 0},
		{"422", 422, nil, `{"message":"` + canary + `"}`, forge.ErrInvalid, 0},
		{"500", 500, nil, `{"message":"` + canary + `"}`, forge.ErrUnavailable, 0},
		{"502", 502, nil, `<html>` + canary + `</html>`, forge.ErrUnavailable, 0},
		{"503", 503, nil, `{"message":"` + canary + `"}`, forge.ErrUnavailable, 0},
		{"429", 429, map[string]string{"Retry-After": "5"}, `{}`, forge.ErrUnavailable, 5 * time.Second},
		{
			"403 for a rate limit", 403,
			map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "7"},
			`{}`,
			forge.ErrUnavailable, 7 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fg := rawForge(t, "", fixedAnswer(tt.status, tt.headers, tt.body))

			_, err := fg.Commit(t.Context(), "acme/infra", oneCommit(rawBase))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Commit = %v, want %v", err, tt.want)
			}

			if strings.Contains(err.Error(), canary) {
				t.Fatalf("the error holds the text of the host: %v", err)
			}

			if got, _ := forge.RetryAfter(err); got != tt.wait {
				t.Fatalf("RetryAfter = %v, want %v", got, tt.wait)
			}
		})
	}
}

func TestCommitSuccessAnswersTheCommit(t *testing.T) {
	t.Parallel()

	fg := rawForge(t, "", func(w http.ResponseWriter, _ *http.Request) { answer(w, 200, goodAnswer()) })

	got, err := fg.Commit(t.Context(), "acme/infra", oneCommit(rawBase))
	if err != nil {
		t.Fatal(err)
	}

	if want := time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC); got.ID != rawCommit || !got.Time.Equal(want) {
		t.Fatalf("Commit = %+v, want %q at %v", got, rawCommit, want)
	}
}

// wireInput is the part of the mutation that the test reads.
type wireInput struct {
	Query     string        `json:"query"`
	Variables wireVariables `json:"variables"`
}

type wireVariables struct {
	Input wireCommitInput `json:"input"`
}

type wireCommitInput struct {
	Branch          wireBranch  `json:"branch"`
	Message         wireMessage `json:"message"`
	FileChanges     wireChanges `json:"fileChanges"`
	ExpectedHeadOid string      `json:"expectedHeadOid"`
}

type wireBranch struct {
	Repository string `json:"repositoryNameWithOwner"`
	Name       string `json:"branchName"`
}

type wireMessage struct {
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

type wireChanges struct {
	Additions []wireAddition `json:"additions"`
	Deletions []wireDeletion `json:"deletions"`
}

type wireAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

type wireDeletion struct {
	Path string `json:"path"`
}

// recorder keeps the last mutation that a server got.
type recorder struct {
	mu   sync.Mutex
	path string
	in   wireInput
}

// serve keeps the request and answers a commit.
func (r *recorder) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.path = req.URL.Path

	if err := json.NewDecoder(req.Body).Decode(&r.in); err != nil {
		answer(w, http.StatusBadRequest, `{}`)

		return
	}

	answer(w, http.StatusOK, goodAnswer())
}

// TestCommitRequestOnTheWire checks the mutation: the address (also for
// GitHub Enterprise Server), the expected head, the branch, the message and the
// changes.
func TestCommitRequestOnTheWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		basePath string
		wantPath string
	}{
		{"github.com", "", "/graphql"},
		{"an enterprise server", "/api/v3", "/api/graphql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := new(recorder)
			c := rawForge(t, tt.basePath, rec.serve)

			req := oneCommit(rawBase)
			req.Changes = append(req.Changes,
				forge.FileChange{Path: "old/b.txt", Content: nil, Delete: true},
				forge.FileChange{Path: "empty.txt", Content: nil, Delete: false},
			)

			if _, err := c.Commit(t.Context(), "acme/infra", req); err != nil {
				t.Fatal(err)
			}

			rec.mu.Lock()
			defer rec.mu.Unlock()

			if rec.path != tt.wantPath || !strings.Contains(rec.in.Query, "createCommitOnBranch") {
				t.Errorf("path %q, query %q; want %q and the mutation", rec.path, rec.in.Query, tt.wantPath)
			}

			checkMutation(t, rec.in.Variables.Input)
		})
	}
}

// checkMutation checks the input of the mutation of oneCommit with two more
// changes: the delete of old/b.txt and an empty file empty.txt.
func checkMutation(t *testing.T, input wireCommitInput) {
	t.Helper()

	if input.ExpectedHeadOid != rawBase || input.Branch != (wireBranch{Repository: "acme/infra", Name: "main"}) {
		t.Errorf("expected head %q, branch %+v", input.ExpectedHeadOid, input.Branch)
	}

	// The digest of the three changes, from an independent Python run.
	wire := rawDelivery("Dustpan-Job: j-1", rawBase)
	wire.Changes = "sha256:1487416319573e10c566b64f810e062e09c02a0d96c4a6a1f3cfa9be3a1d7d96"

	wantBody := "Dustpan-Job: j-1\nCo-authored-by: Ada Lovelace <ada@example.com>\n" +
		testProofLineOf(rawRepoID, wire)
	if input.Message != (wireMessage{Headline: "Change", Body: wantBody}) {
		t.Errorf("message %+v", input.Message)
	}

	wantAdds := []wireAddition{
		{Path: "a.txt", Contents: base64.StdEncoding.EncodeToString([]byte("a"))},
		{Path: "empty.txt", Contents: ""},
	}
	if !slices.Equal(input.FileChanges.Additions, wantAdds) {
		t.Errorf("additions %+v, want %+v", input.FileChanges.Additions, wantAdds)
	}

	if !slices.Equal(input.FileChanges.Deletions, []wireDeletion{{Path: "old/b.txt"}}) {
		t.Errorf("deletions %+v", input.FileChanges.Deletions)
	}
}

// TestCommitMessageHasTheAuthorAsCoAuthor checks the text that GitHub keeps.
func TestCommitMessageHasTheAuthorAsCoAuthor(t *testing.T) {
	t.Parallel()

	ada := forge.Person{Name: "Ada Lovelace", Email: "ada@example.com"}
	line := "Co-authored-by: Ada Lovelace <ada@example.com>"

	tests := []struct {
		name    string
		message string
		author  forge.Person
		want    string
	}{
		{"a trailer block gets the line", "Change\n\nDustpan-Job: j-1", ada, "Change\n\nDustpan-Job: j-1\n" + line},
		{
			"a trailer block of two trailers",
			"Change\n\nDustpan-Job: j-1\nSigned-off-by: Bob <bob@example.com>",
			ada,
			"Change\n\nDustpan-Job: j-1\nSigned-off-by: Bob <bob@example.com>\n" + line,
		},
		{
			"a text and a trailer",
			"Change\n\nSome text\n\nDustpan-Job: j-1\n",
			ada,
			"Change\n\nSome text\n\nDustpan-Job: j-1\n" + line,
		},
		{"CRLF line ends", "Change\r\n\r\nDustpan-Job: j-1\r\n", ada, "Change\n\nDustpan-Job: j-1\n" + line},
		{"no author", "Change\n\nDustpan-Job: j-1", forge.Person{Name: "", Email: ""}, "Change\n\nDustpan-Job: j-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o := openHost(t, forge.AuthToken)
			base := headOf(t, o.host)

			req := oneCommit(base.ID)
			req.Message, req.Author = tt.message, tt.author

			got, err := o.fg.Commit(t.Context(), conformanceRepo, req)
			if err != nil {
				t.Fatal(err)
			}

			head, err := o.host.Head(conformanceRepo, githubfake.DefaultBranch)
			if err != nil || head.ID != got.ID {
				t.Fatalf("head %+v (%v), want the commit %q", head, err, got.ID)
			}

			// The proof is the last line. Its text depends on the repository ID.
			text, proof, found := strings.Cut(head.Message, "\nDustpan-Proof: ")
			if !found || proof == "" || text != tt.want {
				t.Fatalf("message %q, want %q and a proof line", head.Message, tt.want)
			}
		})
	}
}

// TestCommitNeedsATrailerInTheLastBlock gives requests whose trailer the
// adapter could not put at a place that FindCommit reads. Each is invalid, and
// the adapter makes no call.
func TestCommitNeedsATrailerInTheLastBlock(t *testing.T) {
	t.Parallel()

	ada := forge.Person{Name: "Ada Lovelace", Email: "ada@example.com"}

	tests := []struct {
		name    string
		message string
		trailer string
	}{
		{"a headline only", "Change", "Dustpan-Job: j-1"},
		{"a text and no trailer", "Change\n\nSome text", "Dustpan-Job: j-1"},
		{"a trailer in the first paragraph", "Change\n\nDustpan-Job: j-1\n\nSome text", "Dustpan-Job: j-1"},
		{"a last paragraph that is no block of trailers", "Change\n\nDustpan-Job: j-1\nsome text", "Dustpan-Job: j-1"},
		{"a line that is no trailer", "Change\n\nDustpan-Job j-1", "Dustpan-Job: j-1"},
		{"another trailer", "Change\n\nDustpan-Job: j-2", "Dustpan-Job: j-1"},
		{"a trailer that is a prefix", "Change\n\nDustpan-Job: j-10", "Dustpan-Job: j-1"},
		{"a proof line in the message", "Change\n\nDustpan-Job: j-1\nDustpan-Proof: AAAA", "Dustpan-Job: j-1"},
		{"a proof line in other letters", "Change\n\nDustpan-Job: j-1\ndustpan-proof: AAAA", "Dustpan-Job: j-1"},
		{"the trailer is a proof line", "Change\n\nDustpan-Proof: AAAA", "Dustpan-Proof: AAAA"},
		{"no trailer", "Change\n\nDustpan-Job: j-1", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o := openHost(t, forge.AuthToken)
			base := headOf(t, o.host)
			before := o.host.Calls()

			req := oneCommit(base.ID)
			req.Message, req.Trailer, req.Author = tt.message, tt.trailer, ada

			if _, err := o.fg.Commit(t.Context(), conformanceRepo, req); !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("Commit = %v, want ErrInvalid", err)
			}

			if after := o.host.Calls(); after != before {
				t.Fatalf("an invalid commit made %d calls, want none", after-before)
			}
		})
	}
}

// TestCommitTokenOfAnAppWritesOneRepository checks that an app connection
// asks for a token that writes the contents of the one repository, and that it
// reads with a token that reads.
func TestCommitTokenOfAnAppWritesOneRepository(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	base := headOf(t, o.host)

	if _, err := o.fg.Head(t.Context(), conformanceRepo, "main"); err != nil {
		t.Fatal(err)
	}

	if _, err := o.fg.Commit(t.Context(), conformanceRepo, oneCommit(base.ID)); err != nil {
		t.Fatal(err)
	}

	var minted []string

	for _, r := range o.host.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URI, "/access_tokens") && len(r.Body) > 0 {
			minted = append(minted, strings.ReplaceAll(string(r.Body), " ", ""))
		}

		if r.URI == "/graphql" && r.Bearer != githubfake.BearerInstallation {
			t.Errorf("the mutation carried a %s credential, want an installation token", r.Bearer)
		}
	}

	want := []string{
		`{"repositories":["infra"],"permissions":{"contents":"read"}}`,
		`{"repositories":["infra"],"permissions":{"contents":"write"}}`,
	}
	if len(minted) != 2 || minted[0] != want[0] || minted[1] != want[1] {
		t.Fatalf("token requests %v, want %v", minted, want)
	}
}

// TestInstallationThatCannotWriteDoesNotCommit checks that an installation
// with the read permission only lists no commit capability after a check, and
// that a commit before a check is refused.
func TestInstallationThatCannotWriteDoesNotCommit(t *testing.T) {
	t.Parallel()

	read := map[string]string{"contents": "read", "metadata": "read"}

	t.Run("before a check", func(t *testing.T) {
		t.Parallel()

		o := openAppWith(t, read)
		base := headOf(t, o.host)

		_, err := o.fg.Commit(t.Context(), conformanceRepo, oneCommit(base.ID))
		if !errors.Is(err, forge.ErrRefused) {
			t.Fatalf("Commit = %v, want ErrRefused", err)
		}

		if head := headOf(t, o.host); head.ID != base.ID {
			t.Fatalf("head of main = %q, want %q", head.ID, base.ID)
		}
	})

	t.Run("after a check", func(t *testing.T) {
		t.Parallel()

		o := openAppWith(t, read)
		base := headOf(t, o.host)

		if _, err := o.fg.Check(t.Context()); err != nil {
			t.Fatal(err)
		}

		if o.fg.Capabilities().Has(forge.CapCommit) {
			t.Fatal("an installation that cannot write lists the commit capability")
		}

		before := o.host.Calls()

		_, err := o.fg.Commit(t.Context(), conformanceRepo, oneCommit(base.ID))
		if !errors.Is(err, forge.ErrUnsupported) || o.host.Calls() != before {
			t.Fatalf("Commit = %v after %d calls, want ErrUnsupported and no call", err, o.host.Calls()-before)
		}

		_, err = o.fg.FindCommit(
			t.Context(),
			conformanceRepo,
			"main",
			rawDelivery("Dustpan-Job: j-1", base.ID),
			time.Unix(0, 0),
		)
		if !errors.Is(err, forge.ErrUnsupported) {
			t.Fatalf("FindCommit = %v, want ErrUnsupported", err)
		}
	})
}

// commitJSON is one commit of a list.
//
// The message gets the proof line of the job j-1 on the base rawBase, as a commit
// of the connection has, after the last line of the message.
func commitJSON(sha, message string, at time.Time) map[string]any {
	message = strings.TrimRight(message, " \t\r\n") + "\n" + testProofLine(rawRepoID, "Dustpan-Job: j-1", rawBase)

	return map[string]any{
		"sha": sha,
		"commit": map[string]any{
			"message":      message,
			"committer":    map[string]any{"date": at.UTC().Format(time.RFC3339)},
			"verification": map[string]any{"verified": true},
		},
		"author":    map[string]any{"login": rawIdentity},
		"committer": map[string]any{"login": "web-flow"},
	}
}

// shaN returns a valid commit ID for the number n.
func shaN(n int) string { return fmt.Sprintf("%040x", n+1) }

// TestFindCommitReadsHostDataWithCare gives lists with commits that the host
// could forge, and checks which one counts.
func TestFindCommitReadsHostDataWithCare(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	after, before := since.Add(time.Minute), since.Add(-time.Minute)
	trailer := "Dustpan-Job: j-1"

	tests := []struct {
		name string
		list []map[string]any
		raw  string
		want string
		err  error
	}{
		{
			"a trailer in the last paragraph",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\n"+trailer, after)},
			"",
			shaN(1),
			nil,
		},
		{
			"the newest commit with the trailer", []map[string]any{
				commitJSON(shaN(1), "Fix\n\nOther: x", after),
				commitJSON(shaN(2), "Fix\n\n"+trailer, after),
				commitJSON(shaN(3), "Fix\n\n"+trailer, after),
			}, "", shaN(2), nil,
		},
		{
			"a trailer with other trailers",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\nA: b\n"+trailer+"\nC: d\n", after)},
			"",
			shaN(1),
			nil,
		},
		{
			"CRLF line ends",
			[]map[string]any{commitJSON(shaN(1), "Fix\r\n\r\n"+trailer+"\r\n", after)},
			"",
			shaN(1),
			nil,
		},
		{
			"the trailer as the first line",
			[]map[string]any{commitJSON(shaN(1), trailer, after)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"the trailer as a headline",
			[]map[string]any{commitJSON(shaN(1), trailer+"\nmore", after)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"the trailer in a paragraph that is not the last",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\n"+trailer+"\n\nText", after)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"the trailer with a prefix",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\nx"+trailer, after)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"the trailer with a suffix",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\n"+trailer+"0", after)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"the trailer in a quote",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\n> "+trailer, after)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"a commit before the time",
			[]map[string]any{commitJSON(shaN(1), "Fix\n\n"+trailer, before)},
			"",
			"",
			forge.ErrNotFound,
		},
		{
			"a commit before the time, and one after it", []map[string]any{
				commitJSON(shaN(1), "Fix\n\n"+trailer, before),
				commitJSON(shaN(2), "Fix\n\n"+trailer, after),
			}, "", shaN(2), nil,
		},
		{"an empty list", []map[string]any{}, "", "", forge.ErrNotFound},
		{
			"a commit ID that is not valid",
			[]map[string]any{commitJSON("main", "Fix\n\n"+trailer, after)},
			"",
			"",
			forge.ErrUnavailable,
		},
		{
			"a commit ID in capitals",
			[]map[string]any{commitJSON(strings.ToUpper(shaN(0xabcdef)), "Fix\n\n"+trailer, after)},
			"",
			"",
			forge.ErrUnavailable,
		},
		{
			"a commit with no time",
			[]map[string]any{{"sha": shaN(1), "commit": map[string]any{"message": "Fix\n\n" + trailer}}},
			"",
			"",
			forge.ErrUnavailable,
		},
		{"a list that does not parse", nil, `not json`, "", forge.ErrUnavailable},
		{"an object and no list", nil, `{"sha":"` + shaN(1) + `"}`, "", forge.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := tt.raw
			if body == "" {
				data, err := json.Marshal(tt.list)
				if err != nil {
					t.Fatal(err)
				}

				body = string(data)
			}

			fg := rawForge(t, "", func(w http.ResponseWriter, _ *http.Request) { answer(w, 200, body) })

			got, err := fg.FindCommit(
				t.Context(), "acme/infra", "main", rawDelivery(trailer, rawBase), since,
			)
			if !errors.Is(err, tt.err) || got.ID != tt.want {
				t.Fatalf("FindCommit = %+v, %v; want %q and %v", got, err, tt.want, tt.err)
			}
		})
	}
}

// pages serves lists of commits of 100, and keeps the queries.
type pages struct {
	mu      sync.Mutex
	queries []string
	// with is the page whose last commit has the trailer; 0 means none.
	with  int
	since time.Time
}

// newPages returns pages whose page with has the trailer, as pages says.
func newPages(with int, since time.Time) *pages {
	return &pages{mu: sync.Mutex{}, queries: nil, with: with, since: since}
}

// pageJSON returns the page of commits that starts at the number first. The
// last commit of the page has the trailer when trailerLast is above zero.
func (p *pages) pageJSON(first, trailerLast int) (string, error) {
	list := make([]map[string]any, 0, commitsInPage)

	for i := range commitsInPage {
		message := "Fix\n\nOther: " + strconv.Itoa(first+i)
		if trailerLast > 0 && i == commitsInPage-1 {
			message = "Fix\n\nDustpan-Job: j-1"
		}

		list = append(list, commitJSON(shaN(first+i), message, p.since.Add(time.Minute)))
	}

	data, err := json.Marshal(list)

	return string(data), err
}

func (p *pages) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.queries = append(p.queries, r.URL.Path+"?"+r.URL.RawQuery)
	p.mu.Unlock()

	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		answer(w, http.StatusBadRequest, `{}`)

		return
	}

	trailerLast := 0
	if n == p.with {
		trailerLast = 1
	}

	body, err := p.pageJSON((n-1)*commitsInPage, trailerLast)
	if err != nil {
		answer(w, http.StatusInternalServerError, `{}`)

		return
	}

	answer(w, http.StatusOK, body)
}

// commitsInPage is the size of the pages that the adapter asks for.
const commitsInPage = 100

// TestFindCommitAsksForTheRefAndTheTime checks the query, and the way that the
// search goes through pages.
func TestFindCommitAsksForTheRefAndTheTime(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 1, 1, 0, 0, 7, 500, time.UTC)

	tests := []struct {
		name      string
		withOnPg  int
		wantErr   error
		wantID    string
		wantCalls int
	}{
		{"a commit on the second page", 2, nil, shaN(199), 2},
		{"a commit on the fifth page", 5, nil, shaN(499), 5},
		{"no commit in all pages that the adapter reads", 0, forge.ErrUnavailable, "", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newPages(tt.withOnPg, since)
			c := rawForge(t, "", p.serve)

			got, err := c.FindCommit(
				t.Context(),
				"acme/infra",
				rawBranch,
				rawDelivery("Dustpan-Job: j-1", rawBase),
				since,
			)
			if !errors.Is(err, tt.wantErr) || got.ID != tt.wantID {
				t.Fatalf("FindCommit = %+v, %v; want %q and %v", got, err, tt.wantID, tt.wantErr)
			}

			p.mu.Lock()
			defer p.mu.Unlock()

			want := "/repos/acme/infra/commits?sha=main&per_page=100&page=1&since=2026-01-01T00%3A00%3A07Z"
			if len(p.queries) != tt.wantCalls || p.queries[0] != want {
				t.Fatalf("%d calls, want %d; first query %q, want %q", len(p.queries), tt.wantCalls, p.queries, want)
			}
		})
	}
}

// TestFindCommitListLongerThanAPageIsNotRead checks that a list longer than
// the page that was asked for is not read.
func TestFindCommitListLongerThanAPageIsNotRead(t *testing.T) {
	t.Parallel()

	list := make([]map[string]any, 0, commitsInPage+1)
	for i := range commitsInPage + 1 {
		list = append(list, commitJSON(shaN(i), "Fix\n\nDustpan-Job: j-1", time.Now()))
	}

	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}

	c := rawForge(t, "", func(w http.ResponseWriter, _ *http.Request) { answer(w, http.StatusOK, string(data)) })

	_, err = c.FindCommit(
		t.Context(), "acme/infra", "main", rawDelivery("Dustpan-Job: j-1", rawBase), time.Unix(0, 0),
	)
	if !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("FindCommit = %v, want ErrUnavailable", err)
	}
}

// TestCommitFollowsAMovedBranchAtMostThreeTimes gives a branch that moves at
// each call and has the same files at each head. The adapter tries four times
// (one, and three after a move) and then returns ErrConflict.
func TestCommitFollowsAMovedBranchAtMostThreeTimes(t *testing.T) {
	t.Parallel()

	var (
		mu        sync.Mutex
		mutations int
		heads     int
	)

	c := rawForge(t, "", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case strings.Contains(r.URL.Path, "/git/ref/heads/"):
			heads++

			answer(w, http.StatusOK, `{"object":{"sha":"`+shaN(1000+heads)+`"}}`)
		case strings.Contains(r.URL.Path, "/compare/"):
			answer(w, http.StatusOK, aheadOf(rawBase))
		case strings.Contains(r.URL.Path, "/git/trees/"):
			answer(w, http.StatusOK, `{"tree":[]}`)
		case r.URL.Path == "/graphql":
			mutations++

			answer(w, http.StatusOK, typedErrors("x", "STALE_DATA"))
		default:
			answer(w, http.StatusNotFound, `{"message":"Not Found"}`)
		}
	})

	_, err := c.Commit(t.Context(), "acme/infra", oneCommit(rawBase))
	if !errors.Is(err, forge.ErrConflict) {
		t.Fatalf("Commit = %v, want ErrConflict", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if mutations != 4 || heads != 3 {
		t.Fatalf("%d mutations and %d reads of the head, want 4 and 3", mutations, heads)
	}
}

// aheadOf is the answer of the compare call for a head that is ahead of base.
func aheadOf(base string) string {
	return `{"status":"ahead","merge_base_commit":{"sha":"` + base + `"}}`
}

// TestCommitNeedsTheBaseToBeAnAncestor gives a branch that moved and a compare
// answer of each kind. Only a base that is an ancestor of the new head lets
// the commit go on. Any other answer makes no second mutation.
func TestCommitNeedsTheBaseToBeAnAncestor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		want    error
		retries int
	}{
		{"ahead", 200, aheadOf(rawBase), nil, 1},
		{"identical", 200, `{"status":"identical","merge_base_commit":{"sha":"` + rawBase + `"}}`, nil, 1},
		{"diverged", 200, `{"status":"diverged","merge_base_commit":{"sha":"` + rawBase + `"}}`, forge.ErrConflict, 0},
		{"behind", 200, `{"status":"behind","merge_base_commit":{"sha":"` + rawBase + `"}}`, forge.ErrConflict, 0},
		{"ahead with another merge base", 200, aheadOf(rawCommit), forge.ErrConflict, 0},
		{"ahead with no merge base", 200, `{"status":"ahead"}`, forge.ErrConflict, 0},
		{
			"a status that is not known",
			200,
			`{"status":"new","merge_base_commit":{"sha":"` + rawBase + `"}}`,
			forge.ErrConflict,
			0,
		},
		{"an answer that does not parse", 200, `not json`, forge.ErrConflict, 0},
		{"a base that GitHub does not have", 404, `{"message":"Not Found"}`, forge.ErrConflict, 0},
		{"a server error", 503, `{}`, forge.ErrUnavailable, 0},
		{"a refused credential", 401, `{}`, forge.ErrAuth, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu        sync.Mutex
				mutations int
			)

			c := rawForge(t, "", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()

				switch {
				case strings.Contains(r.URL.Path, "/git/ref/heads/"):
					answer(w, http.StatusOK, `{"object":{"sha":"`+rawCommit+`"}}`)
				case strings.Contains(r.URL.Path, "/compare/"):
					answer(w, tt.status, tt.body)
				case strings.Contains(r.URL.Path, "/git/trees/"):
					answer(w, http.StatusOK, `{"tree":[]}`)
				case mutations == 0:
					mutations++

					answer(w, http.StatusOK, typedErrors("x", "STALE_DATA"))
				default:
					mutations++

					answer(w, http.StatusOK, goodAnswer())
				}
			})

			_, err := c.Commit(t.Context(), "acme/infra", oneCommit(rawBase))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Commit = %v, want %v", err, tt.want)
			}

			mu.Lock()
			defer mu.Unlock()

			if mutations != 1+tt.retries {
				t.Fatalf("%d mutations, want %d", mutations, 1+tt.retries)
			}
		})
	}
}
