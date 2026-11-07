package github_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

const (
	headSHA  = "1111111111111111111111111111111111111111"
	baseSHA  = "2222222222222222222222222222222222222222"
	mergeSHA = "3333333333333333333333333333333333333333"
)

// canned is a host that answers each call by its method and path from a table,
// and records the calls.
type canned struct {
	forge.Forge

	mu      sync.Mutex
	answers map[string]hostAnswer
	seen    []string
}

// answer is the status and the body of one call of a canned host.
type hostAnswer struct {
	status int
	body   string
	header map[string]string
	// next replaces this answer after the host served it once.
	next *hostAnswer
}

// none is an answer that a test does not use.
func none() hostAnswer { return hostAnswer{status: 0, body: "", header: nil, next: nil} }

func okAnswer(body any) hostAnswer {
	return hostAnswer{status: http.StatusOK, body: toJSON(body), header: nil, next: nil}
}

func statusAnswer(code int, body string) hostAnswer {
	return hostAnswer{status: code, body: body, header: nil, next: nil}
}

func toJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func (c *canned) serve(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path

	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return
	}

	c.mu.Lock()
	c.seen = append(c.seen, key)

	// An answer for the method, the path and the query is more specific than an
	// answer for the method and the path.
	if _, specific := c.answers[key+"?"+r.URL.RawQuery]; specific {
		key += "?" + r.URL.RawQuery
	}

	a, found := c.answers[key]

	if found && a.next != nil {
		c.answers[key] = *a.next
	}
	c.mu.Unlock()

	if !found {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	for name, value := range a.header {
		w.Header().Set(name, value)
	}

	w.WriteHeader(a.status)

	if _, err := w.Write([]byte(a.body)); err != nil {
		return
	}
}

// calls returns the calls that the host got, as "METHOD /path".
func (c *canned) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.seen...)
}

// sent reports whether the host got a call with the method.
func (c *canned) sent(method string) bool {
	for _, call := range c.calls() {
		if strings.HasPrefix(call, method+" ") {
			return true
		}
	}

	return false
}

// cannedForge returns a token connection to a host that answers from answers.
func cannedForge(t *testing.T, answers map[string]hostAnswer) *canned {
	t.Helper()

	c := &canned{Forge: nil, mu: sync.Mutex{}, answers: answers, seen: nil}
	srv := httptest.NewTLSServer(http.HandlerFunc(c.serve))
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	o, err := github.NewOpener(github.Config{
		Rules: egress.Rules{
			Allow:            nil,
			LoopbackForTests: true,
		},
		RootCAs:   pool,
		Now:       nil,
		Prover:    testProver{},
		AppBudget: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	conn := forge.Connection{
		ID: "c1", Kind: forge.KindGitHub, Auth: forge.AuthToken, BaseURL: srv.URL, Params: nil, Push: true,
	}
	cred := forge.Credential{SetID: "s", Secret: []byte("github_pat_x"), CreatedAt: time.Time{}}

	f, err := o.Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	c.Forge = f

	return c
}

// pullJSON returns the answer for an open pull request that passes every check.
func pullJSON() map[string]any {
	return map[string]any{
		"number": 7, "state": "open", "html_url": "https://github.example/acme/infra/pull/7",
		"user": map[string]any{"id": 55, "login": "dustpan[bot]"}, "merged": false, "merged_at": nil,
		"merge_commit_sha": mergeSHA, "mergeable": true, "mergeable_state": "clean",
		"base": map[string]any{"ref": "main", "sha": baseSHA, "repo": map[string]any{"id": 900}},
		"head": map[string]any{"ref": "dustpan/j-1", "sha": headSHA, "repo": map[string]any{"id": 900}},
	}
}

// pullWith returns pullJSON with the changes of edit.
func pullWith(edit func(map[string]any)) map[string]any {
	p := pullJSON()
	edit(p)

	return p
}

// pullEnd returns the map of an end of the pull request.
func pullEnd(p map[string]any, name string) map[string]any {
	m, isMap := p[name].(map[string]any)
	if !isMap {
		panic("the test pull request has no " + name)
	}

	return m
}

const (
	branchPath = "GET /repos/acme/infra/branches/dustpan/j-1"
	pullPath   = "GET /repos/acme/infra/pulls/7"
	patchPath  = "PATCH /repos/acme/infra/pulls/7"
	listPath   = "GET /repos/acme/infra/pulls"
	postPath   = "POST /repos/acme/infra/pulls"
	repoPath   = "GET /repos/acme/infra"
	refPath    = "DELETE /repos/acme/infra/git/refs/heads/dustpan/j-1"
)

// TestReadReviewRefusesAnswersThatFailACheck sends an answer to ReadReview
// that fails one check at a time. The adapter must return ErrUnavailable and
// never a review request.
func TestReadReviewRefusesAnswersThatFailACheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"a number of another review request", func(p map[string]any) { p["number"] = 8 }},
		{"a number of zero", func(p map[string]any) { p["number"] = 0 }},
		{"a number that is text", func(p map[string]any) { p["number"] = "7" }},
		{"a state that GitHub does not have", func(p map[string]any) { p["state"] = "draft" }},
		{"an open state with the merged flag", func(p map[string]any) { p["merged"] = true }},
		{"an open state with a merge time", func(p map[string]any) { p["merged_at"] = "2026-01-01T00:00:00Z" }},
		{"a closed state with a merge time and no flag", func(p map[string]any) {
			p["state"], p["merged_at"] = "closed", "2026-01-01T00:00:00Z"
		}},
		{"a merge without a merge time", func(p map[string]any) { p["state"], p["merged"] = "closed", true }},
		{"a merge without a merge commit", func(p map[string]any) {
			p["state"], p["merged"], p["merged_at"], p["merge_commit_sha"] = "closed", true, "2026-01-01T00:00:00Z", nil
		}},
		{"a merge with a merge commit that is not an ID", func(p map[string]any) {
			p["state"], p["merged"], p["merged_at"], p["merge_commit_sha"] = "closed", true, "2026-01-01T00:00:00Z", "abc"
		}},
		{"a test merge commit that is not an ID", func(p map[string]any) { p["merge_commit_sha"] = "../../etc" }},
		{
			"a head commit with capital letters",
			func(p map[string]any) { pullEnd(p, "head")["sha"] = strings.Repeat("A", 40) },
		},
		{"a head commit that is short", func(p map[string]any) { pullEnd(p, "head")["sha"] = "abc123" }},
		{"a base commit that is empty", func(p map[string]any) { pullEnd(p, "base")["sha"] = "" }},
		{"a head branch that leaves the repository", func(p map[string]any) { pullEnd(p, "head")["ref"] = "../main" }},
		{"a base branch that is a full ref", func(p map[string]any) { pullEnd(p, "base")["ref"] = "refs/heads/main" }},
		{"a base without a repository", func(p map[string]any) { pullEnd(p, "base")["repo"] = nil }},
		{
			"a base repository ID of zero",
			func(p map[string]any) { pullEnd(p, "base")["repo"] = map[string]any{"id": 0} },
		},
		{
			"a head repository ID that is text",
			func(p map[string]any) { pullEnd(p, "head")["repo"] = map[string]any{"id": "x"} },
		},
		{
			"a head repository ID that is negative",
			func(p map[string]any) { pullEnd(p, "head")["repo"] = map[string]any{"id": -4} },
		},
		{"no author", func(p map[string]any) { p["user"] = nil }},
		{"an author ID of zero", func(p map[string]any) { p["user"] = map[string]any{"id": 0} }},
		{"an address that is not https", func(p map[string]any) { p["html_url"] = "http://github.example/x" }},
		{"an address with a script", func(p map[string]any) { p["html_url"] = "javascript:alert(1)" }},
		{"an empty address", func(p map[string]any) { p["html_url"] = "" }},
		{
			"an address with a password",
			func(p map[string]any) { p["html_url"] = fakecred.URL("https", "u", "p", "github.example", "/x") },
		},
		{"an address that is too long", func(p map[string]any) {
			p["html_url"] = "https://github.example/" + strings.Repeat("a", 3000)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{pullPath: okAnswer(pullWith(tt.edit))})

			got, err := f.ReadReview(t.Context(), "acme/infra", "7")
			if !errors.Is(err, forge.ErrUnavailable) {
				t.Fatalf("ReadReview = %+v, %v; want ErrUnavailable", got, err)
			}

			var zero forge.Review
			if got != zero {
				t.Fatalf("ReadReview = %+v with an error, want the zero review request", got)
			}
		})
	}
}

// TestReadReviewMergeState checks the mapping of the merge state, and that
// only a clean state with the flag set to true is clean.
func TestReadReviewMergeState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		state     string
		mergeable any
		want      forge.MergeState
		testMerge string
	}{
		{"clean and mergeable", "clean", true, forge.MergeClean, mergeSHA},
		{"clean with the flag not computed", "clean", nil, forge.MergeUnknown, ""},
		{"clean with the flag false", "clean", false, forge.MergeUnknown, ""},
		{"unknown", "unknown", nil, forge.MergeUnknown, ""},
		{"unknown with a flag and a stale commit", "unknown", true, forge.MergeUnknown, ""},
		{"dirty", "dirty", false, forge.MergeConflict, ""},
		{"blocked", "blocked", true, forge.MergeBlocked, mergeSHA},
		{"behind", "behind", true, forge.MergeBehind, mergeSHA},
		{"unstable", "unstable", true, forge.MergeUnstable, mergeSHA},
		{"draft", "draft", true, forge.MergeDraft, mergeSHA},
		{"a state that the adapter does not know", "quantum", true, forge.MergeUnknown, ""},
		{"a state with other letter case", "Clean", true, forge.MergeUnknown, ""},
		{"no state", "", true, forge.MergeUnknown, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := pullWith(func(p map[string]any) { p["mergeable_state"], p["mergeable"] = tt.state, tt.mergeable })
			f := cannedForge(t, map[string]hostAnswer{pullPath: okAnswer(p)})

			got, err := f.ReadReview(t.Context(), "acme/infra", "7")
			if err != nil {
				t.Fatalf("ReadReview = %v", err)
			}

			if got.Mergeable != tt.want || got.TestMergeCommit != tt.testMerge {
				t.Fatalf("ReadReview mergeable, test merge = %q, %q; want %q, %q",
					got.Mergeable, got.TestMergeCommit, tt.want, tt.testMerge)
			}
		})
	}
}

// TestReadReviewShowsTheHead checks the fields of a good answer, and that a head
// in another repository, or in no repository, is a fork.
func TestReadReviewShowsTheHead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		edit     func(map[string]any)
		wantFork bool
		wantHead string
	}{
		{"the same repository", func(map[string]any) {}, false, "900"},
		{
			"another repository",
			func(p map[string]any) { pullEnd(p, "head")["repo"] = map[string]any{"id": 901} },
			true,
			"901",
		},
		{"a fork that was deleted", func(p map[string]any) { pullEnd(p, "head")["repo"] = nil }, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{pullPath: okAnswer(pullWith(tt.edit))})

			got, err := f.ReadReview(t.Context(), "acme/infra", "7")
			if err != nil {
				t.Fatalf("ReadReview = %v", err)
			}

			if got.FromFork() != tt.wantFork || got.HeadRepoID != tt.wantHead || got.BaseRepoID != "900" {
				t.Fatalf("ReadReview repositories = %q, %q; fork %v; want %q and fork %v",
					got.BaseRepoID, got.HeadRepoID, got.FromFork(), tt.wantHead, tt.wantFork)
			}

			want := forge.Review{
				ID:              "7",
				URL:             "https://github.example/acme/infra/pull/7",
				State:           forge.ReviewOpen,
				Branch:          "dustpan/j-1",
				Target:          "main",
				Head:            headSHA,
				BaseCommit:      baseSHA,
				BaseRepoID:      "900",
				HeadRepoID:      tt.wantHead,
				AuthorID:        "55",
				TestMergeCommit: mergeSHA,
				Mergeable:       forge.MergeClean,
				MergedAt:        time.Time{},
				MergeCommit:     "",
			}
			if got != want {
				t.Fatalf("ReadReview = %+v, want %+v", got, want)
			}
		})
	}
}

// TestReadReviewMerged checks the answer for a merged pull request.
func TestReadReviewMerged(t *testing.T) {
	t.Parallel()

	p := pullWith(func(p map[string]any) {
		p["state"], p["merged"], p["merged_at"], p["mergeable"], p["mergeable_state"], p["merge_commit_sha"] =
			"closed", true, "2026-02-03T04:05:06Z", nil, "unknown", mergeSHA
	})
	f := cannedForge(t, map[string]hostAnswer{pullPath: okAnswer(p)})

	got, err := f.ReadReview(t.Context(), "acme/infra", "7")
	if err != nil {
		t.Fatalf("ReadReview = %v", err)
	}

	wantTime := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	if got.State != forge.ReviewMerged || !got.MergedAt.Equal(wantTime) || got.MergeCommit != mergeSHA ||
		got.TestMergeCommit != "" || got.Mergeable != forge.MergeUnknown {
		t.Fatalf("ReadReview = %+v, want a merge at %v with the merge commit %s", got, wantTime, mergeSHA)
	}
}

// TestReadReviewStatus checks the map from the status of GitHub to the errors
// of the port, for the calls of the review methods.
func TestReadReviewStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer hostAnswer
		want   error
	}{
		{"not found", statusAnswer(404, `{"message":"Not Found"}`), forge.ErrNotFound},
		{"unprocessable", statusAnswer(422, `{"message":"Validation Failed"}`), forge.ErrInvalid},
		{"unprocessable with a body that does not parse", statusAnswer(422, `<html>`), forge.ErrInvalid},
		{"server error", statusAnswer(500, ""), forge.ErrUnavailable},
		{"bad gateway", statusAnswer(502, ""), forge.ErrUnavailable},
		{"unauthorized", statusAnswer(401, `{"message":"Bad credentials"}`), forge.ErrAuth},
		{"forbidden", statusAnswer(403, `{"message":"Resource not accessible"}`), forge.ErrRefused},
		{"rate limited", statusAnswer(429, ""), forge.ErrUnavailable},
		{"an unknown status", statusAnswer(418, ""), forge.ErrUnavailable},
		{"a redirect", statusAnswer(301, ""), forge.ErrUnavailable},
		{"a body that does not parse", statusAnswer(200, "not json {"), forge.ErrUnavailable},
		{"an empty body", statusAnswer(200, ""), forge.ErrUnavailable},
		{"a list instead of an object", statusAnswer(200, "[]"), forge.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{pullPath: tt.answer})

			_, err := f.ReadReview(t.Context(), "acme/infra", "7")
			if !errors.Is(err, tt.want) {
				t.Fatalf("ReadReview = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestReadReviewCapsTheAnswer checks the size limit of the adapter, which is
// lower than the limit of the egress rules, and the limit of the egress rules.
func TestReadReviewCapsTheAnswer(t *testing.T) {
	t.Parallel()

	for _, size := range []int{2 << 20, 5 << 20} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()

			p := pullWith(func(p map[string]any) { p["body"] = strings.Repeat("x", size) })
			f := cannedForge(t, map[string]hostAnswer{pullPath: okAnswer(p)})

			if _, err := f.ReadReview(t.Context(), "acme/infra", "7"); !errors.Is(err, forge.ErrUnavailable) {
				t.Fatalf("ReadReview of %d bytes = %v, want ErrUnavailable", size, err)
			}
		})
	}
}

// TestOpenReviewRefusesAnswersForAnotherReview checks that the answer to the
// call that opens a pull request is the pull request that was asked for.
func TestOpenReviewRefusesAnswersForAnotherReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"another branch", func(p map[string]any) { pullEnd(p, "head")["ref"] = "dustpan/j-2" }},
		{"another target", func(p map[string]any) { pullEnd(p, "base")["ref"] = "release" }},
		{"a closed pull request", func(p map[string]any) { p["state"] = "closed" }},
		{"a head in a fork", func(p map[string]any) { pullEnd(p, "head")["repo"] = map[string]any{"id": 901} }},
		{"a head with no repository", func(p map[string]any) { pullEnd(p, "head")["repo"] = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{
				listPath: okAnswer([]any{}),
				postPath: {status: http.StatusCreated, body: toJSON(pullWith(tt.edit)), header: nil, next: nil},
			})

			_, err := f.OpenReview(t.Context(), "acme/infra", reviewRequest())
			if !errors.Is(err, forge.ErrUnavailable) {
				t.Fatalf("OpenReview = %v, want ErrUnavailable", err)
			}
		})
	}
}

func reviewRequest() forge.ReviewRequest {
	return forge.ReviewRequest{Branch: "dustpan/j-1", Target: "main", Title: "Update the stacks", Body: "Body.\n"}
}

// listItem is an item of the list of pull requests that GitHub answers.
func listItem(number int, head, base string, repo int) map[string]any {
	return map[string]any{
		"number": number,
		"head":   map[string]any{"ref": head, "repo": map[string]any{"id": repo}},
		"base":   map[string]any{"ref": base, "repo": map[string]any{"id": 900}},
	}
}

func created() hostAnswer {
	return hostAnswer{status: http.StatusCreated, body: toJSON(pullJSON()), header: nil, next: nil}
}

// TestOpenReviewSkipsAnItemOfAFork checks that an open pull request of a fork,
// with a branch of the same name, is not the review request of the write-back.
func TestOpenReviewSkipsAnItemOfAFork(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		listPath: okAnswer([]any{listItem(3, "dustpan/j-1", "main", 901)}), postPath: created(),
	})

	got, err := f.OpenReview(t.Context(), "acme/infra", reviewRequest())
	if err != nil || got.ID != "7" || !f.sent(http.MethodPost) {
		t.Fatalf("OpenReview = %+v, %v; calls %v; want a new pull request 7", got, err, f.calls())
	}
}

// TestOpenReviewSkipsAnItemOfAnotherBranch checks that an item of another
// branch or another target is not the review request.
func TestOpenReviewSkipsAnItemOfAnotherBranch(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		listPath: okAnswer([]any{listItem(3, "dustpan/j-9", "main", 900), listItem(4, "dustpan/j-1", "release", 900)}),
		postPath: created(),
	})

	got, err := f.OpenReview(t.Context(), "acme/infra", reviewRequest())
	if err != nil || got.ID != "7" || !f.sent(http.MethodPost) {
		t.Fatalf("OpenReview = %+v, %v; want a new pull request 7", got, err)
	}
}

// TestOpenReviewReturnsTheOpenOne checks that an open pull request of the
// branch is read and returned, and that no second one is opened.
func TestOpenReviewReturnsTheOpenOne(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		listPath: okAnswer([]any{listItem(7, "dustpan/j-1", "main", 900)}), pullPath: okAnswer(pullJSON()),
	})

	got, err := f.OpenReview(t.Context(), "acme/infra", reviewRequest())
	if err != nil || got.ID != "7" || f.sent(http.MethodPost) {
		t.Fatalf("OpenReview = %+v, %v; calls %v; want the pull request 7 and no POST", got, err, f.calls())
	}
}

// TestOpenReviewDistrustsALongList checks that a list above the page size is
// not trusted.
func TestOpenReviewDistrustsALongList(t *testing.T) {
	t.Parallel()

	many := make([]any, 0, 6)
	for i := range 6 {
		many = append(many, listItem(i+10, "dustpan/j-9", "main", 900))
	}

	f := cannedForge(t, map[string]hostAnswer{listPath: okAnswer(many)})

	if _, err := f.OpenReview(t.Context(), "acme/infra", reviewRequest()); !errors.Is(err, forge.ErrUnavailable) ||
		f.sent(http.MethodPost) {
		t.Fatalf("OpenReview = %v, calls %v; want ErrUnavailable and no POST", err, f.calls())
	}
}

// TestOpenReviewStatus checks the map from the answer of GitHub for the call
// that opens a pull request.
func TestOpenReviewStatus(t *testing.T) {
	t.Parallel()

	validation := func(errs string) hostAnswer {
		return statusAnswer(http.StatusUnprocessableEntity, `{"message":"Validation Failed","errors":[`+errs+`]}`)
	}

	tests := []struct {
		name   string
		answer hostAnswer
		want   error
	}{
		{"a head that is not a branch", validation(`{"field":"head","code":"invalid"}`), forge.ErrNotFound},
		{"a base that is not a branch", validation(`{"field":"base","code":"invalid"}`), forge.ErrNotFound},
		{
			"no commit between",
			validation(`{"code":"custom","message":"No commits between main and x"}`),
			forge.ErrRefused,
		},
		{"another problem", validation(`{"field":"title","code":"missing_field"}`), forge.ErrInvalid},
		{"a body that does not parse", statusAnswer(422, "<html>"), forge.ErrInvalid},
		{
			"a missing permission",
			statusAnswer(403, `{"message":"Resource not accessible by integration"}`),
			forge.ErrRefused,
		},
		{"a missing repository", statusAnswer(404, `{"message":"Not Found"}`), forge.ErrNotFound},
		{"a server error", statusAnswer(503, ""), forge.ErrUnavailable},
		{"a refused credential", statusAnswer(401, ""), forge.ErrAuth},
		{"an answer that does not parse", statusAnswer(201, "{"), forge.ErrUnavailable},
		{
			"a pull request that exists, and none of ours in the list",
			validation(`{"code":"custom","message":"A pull request already exists for acme:x."}`), forge.ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{listPath: okAnswer([]any{}), postPath: tt.answer})

			_, err := f.OpenReview(t.Context(), "acme/infra", reviewRequest())
			if !errors.Is(err, tt.want) {
				t.Fatalf("OpenReview = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCloseReviewRefusesOtherReviews checks that a pull request that is not one
// of the write-back is never closed, and that the answer to the close is read.
func TestCloseReviewRefusesOtherReviews(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		edit   func(map[string]any)
		patch  hostAnswer
		want   error
		closes bool
	}{
		{
			"a branch of a person",
			func(p map[string]any) { pullEnd(p, "head")["ref"] = "feature/x" },
			none(),
			forge.ErrRefused,
			false,
		},
		{
			"a branch with the prefix inside",
			func(p map[string]any) { pullEnd(p, "head")["ref"] = "x/dustpan/j" },
			none(),
			forge.ErrRefused,
			false,
		},
		{
			"a fork",
			func(p map[string]any) { pullEnd(p, "head")["repo"] = map[string]any{"id": 901} },
			none(),
			forge.ErrRefused,
			false,
		},
		{
			"a deleted fork",
			func(p map[string]any) { pullEnd(p, "head")["repo"] = nil },
			none(),
			forge.ErrRefused,
			false,
		},
		{
			"a pull request of the write-back",
			func(map[string]any) {},
			okAnswer(pullWith(func(p map[string]any) { p["state"] = "closed" })),
			nil,
			true,
		},
		{
			"a close that GitHub answers as still open", func(map[string]any) {},
			okAnswer(pullJSON()), forge.ErrUnavailable, true,
		},
		{
			"a close that answers as merged", func(map[string]any) {},
			okAnswer(pullWith(func(p map[string]any) {
				p["state"], p["merged"], p["merged_at"] = "closed", true, "2026-01-01T00:00:00Z"
			})), forge.ErrConflict, true,
		},
		{
			"a close that GitHub refuses as invalid",
			func(map[string]any) {},
			statusAnswer(422, `{"message":"x"}`),
			forge.ErrInvalid,
			true,
		},
		{
			"a close that the credential cannot do",
			func(map[string]any) {},
			statusAnswer(403, ""),
			forge.ErrRefused,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			answers := map[string]hostAnswer{pullPath: okAnswer(pullWith(tt.edit))}
			if tt.closes {
				answers[patchPath] = tt.patch
			}

			f := cannedForge(t, answers)

			err := f.CloseReview(t.Context(), "acme/infra", "7")
			if !errors.Is(err, tt.want) && (tt.want != nil || err != nil) {
				t.Fatalf("CloseReview = %v, want %v", err, tt.want)
			}

			if sent := f.sent(http.MethodPatch); sent != tt.closes {
				t.Fatalf("CloseReview sent a PATCH = %v, want %v; calls %v", sent, tt.closes, f.calls())
			}
		})
	}
}

// TestCloseReviewAfterAMerge checks that a close that GitHub refuses because
// the pull request merged after the read is a conflict.
func TestCloseReviewAfterAMerge(t *testing.T) {
	t.Parallel()

	merged := pullWith(func(p map[string]any) {
		p["state"], p["merged"], p["merged_at"] = "closed", true, "2026-01-01T00:00:00Z"
	})
	again := okAnswer(merged)

	f := cannedForge(t, map[string]hostAnswer{
		pullPath:  {status: http.StatusOK, body: toJSON(pullJSON()), header: nil, next: &again},
		patchPath: statusAnswer(http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`),
	})

	if err := f.CloseReview(t.Context(), "acme/infra", "7"); !errors.Is(err, forge.ErrConflict) {
		t.Fatalf("CloseReview = %v, want ErrConflict", err)
	}
}

// TestDeleteBranchChecksTheDefaultBranch checks, with a branch that has the
// prefix of the write-back, that the default branch is never deleted and that
// no delete follows an answer about the repository that cannot be trusted.
func TestDeleteBranchChecksTheDefaultBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		repo   hostAnswer
		want   error
		delete bool
	}{
		{
			"the branch is the default branch",
			okAnswer(map[string]any{"default_branch": "dustpan/j-1"}),
			forge.ErrRefused,
			false,
		},
		{"another default branch", okAnswer(map[string]any{"default_branch": "main"}), nil, true},
		{"no default branch in the answer", okAnswer(map[string]any{}), forge.ErrUnavailable, false},
		{
			"a default branch that is not a name",
			okAnswer(map[string]any{"default_branch": "../x"}),
			forge.ErrUnavailable,
			false,
		},
		{"an answer that does not parse", statusAnswer(200, "not json"), forge.ErrUnavailable, false},
		{"a server error", statusAnswer(500, ""), forge.ErrUnavailable, false},
		{"a missing repository", statusAnswer(404, ""), forge.ErrNotFound, false},
		{"a refused credential", statusAnswer(401, ""), forge.ErrAuth, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, deletable(map[string]hostAnswer{
				repoPath: tt.repo, refPath: {status: http.StatusNoContent, body: "", header: nil, next: nil},
			}))

			err := f.DeleteBranch(t.Context(), "acme/infra", "dustpan/j-1")
			if !errors.Is(err, tt.want) && (tt.want != nil || err != nil) {
				t.Fatalf("DeleteBranch = %v, want %v", err, tt.want)
			}

			if sent := f.sent(http.MethodDelete); sent != tt.delete {
				t.Fatalf("DeleteBranch sent a DELETE = %v, want %v; calls %v", sent, tt.delete, f.calls())
			}
		})
	}
}

// TestDeleteBranchRefusesOtherBranchesWithNoCall checks the check of the prefix
// alone: the host would delete any branch, and says that the default branch is
// another one, so only the check of the adapter stops the call.
func TestDeleteBranchRefusesOtherBranchesWithNoCall(t *testing.T) {
	t.Parallel()

	for _, branch := range []string{"main", "master", "release/1", "feature/dustpan/x", "dustpan", "dustpan/", "DUSTPAN/j-1"} {
		t.Run(branch, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{
				repoPath: okAnswer(map[string]any{"default_branch": "trunk"}),
				"DELETE /repos/acme/infra/git/refs/heads/" + branch: {
					status: http.StatusNoContent,
					body:   "",
					header: nil,
					next:   nil,
				},
			})

			err := f.DeleteBranch(t.Context(), "acme/infra", branch)
			if !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("DeleteBranch(%q) = %v, want ErrInvalid", branch, err)
			}

			if calls := f.calls(); len(calls) != 0 {
				t.Fatalf("DeleteBranch(%q) made the calls %v, want none", branch, calls)
			}
		})
	}
}

// TestDeleteBranchStatus checks the map from the answer of the delete call.
func TestDeleteBranchStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer hostAnswer
		want   error
	}{
		{"deleted", statusAnswer(204, ""), nil},
		{"a success status other than 204", statusAnswer(200, "{}"), forge.ErrUnavailable},
		{
			"a branch that does not exist",
			statusAnswer(422, `{"message":"Reference does not exist"}`),
			forge.ErrNotFound,
		},
		{
			"a protected branch",
			statusAnswer(422, `{"message":"Cannot delete this protected branch"}`),
			forge.ErrRefused,
		},
		{"another problem", statusAnswer(422, `{"message":"Something"}`), forge.ErrInvalid},
		{"a problem that does not parse", statusAnswer(422, "<html>"), forge.ErrInvalid},
		{"a missing permission", statusAnswer(403, `{"message":"Resource not accessible"}`), forge.ErrRefused},
		{"a missing repository", statusAnswer(404, ""), forge.ErrNotFound},
		{"a server error", statusAnswer(500, ""), forge.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, deletable(map[string]hostAnswer{
				repoPath: okAnswer(map[string]any{"default_branch": "main"}), refPath: tt.answer,
			}))

			err := f.DeleteBranch(t.Context(), "acme/infra", "dustpan/j-1")
			if !errors.Is(err, tt.want) && (tt.want != nil || err != nil) {
				t.Fatalf("DeleteBranch = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestReviewMethodsNeedThePullRequestPermission checks that an installation
// without the permission to write pull requests lists no review capability,
// that the review methods are unsupported, and that the token for a review
// request asks for the pull requests and the contents, both for write.
func TestReviewMethodsNeedThePullRequestPermission(t *testing.T) {
	t.Parallel()

	host := newHostWith(t, func(c *githubfake.Config) {
		c.Permissions = map[string]string{"contents": "write", "metadata": "read"}
	})
	o := newHostOpener(t, host)
	conn, cred := hostConnection(t, host, forge.AuthApp)

	f, err := o.Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	if _, checkErr := f.Check(t.Context()); checkErr != nil {
		t.Fatalf("Check = %v", checkErr)
	}

	caps := f.Capabilities()
	if caps.Has(forge.CapReview) || !caps.Has(forge.CapCommit) {
		t.Fatalf("Capabilities = %v, want commit and no review", caps)
	}

	for name, err := range map[string]error{
		"OpenReview":   second(f.OpenReview(t.Context(), conformanceRepo, reviewRequest())),
		"ReadReview":   second(f.ReadReview(t.Context(), conformanceRepo, "1")),
		"CloseReview":  f.CloseReview(t.Context(), conformanceRepo, "1"),
		"DeleteBranch": f.DeleteBranch(t.Context(), conformanceRepo, "dustpan/j-1"),
	} {
		if !errors.Is(err, forge.ErrUnsupported) {
			t.Errorf("%s = %v, want ErrUnsupported", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

// TestTokenPermissionsOfEachMethod checks the permissions in the token request
// of an app for each method: a method that only reads asks for read
// permissions, and a method that writes asks for the write of the contents and
// of the pull requests.
func TestTokenPermissionsOfEachMethod(t *testing.T) {
	t.Parallel()

	const (
		readOnly  = `{"repositories":["infra"],"permissions":{"contents":"read","pull_requests":"read"}}`
		writeBoth = `{"repositories":["infra"],"permissions":{"contents":"write","pull_requests":"write"}}`
		contents  = `{"repositories":["infra"],"permissions":{"contents":"read"}}`
	)

	tests := []struct {
		name string
		call func(ctx context.Context, f forge.Forge) error
		want string
	}{
		{"Head", func(ctx context.Context, f forge.Forge) error {
			return second(f.Head(ctx, conformanceRepo, "main"))
		}, contents},
		{"ReadReview", func(ctx context.Context, f forge.Forge) error {
			return second(f.ReadReview(ctx, conformanceRepo, "1"))
		}, readOnly},
		{"OpenReview", func(ctx context.Context, f forge.Forge) error {
			return second(f.OpenReview(ctx, conformanceRepo, reviewRequest()))
		}, writeBoth},
		{"CloseReview", func(ctx context.Context, f forge.Forge) error {
			return f.CloseReview(ctx, conformanceRepo, "1")
		}, writeBoth},
		{"DeleteBranch", func(ctx context.Context, f forge.Forge) error {
			return f.DeleteBranch(ctx, conformanceRepo, "dustpan/x")
		}, writeBoth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := newHost(t)
			o := newHostOpener(t, host)
			conn, cred := hostConnection(t, host, forge.AuthApp)

			f, err := o.Open(t.Context(), conn, cred)
			if err != nil {
				t.Fatal(err)
			}

			// The call fails (the repository has no such object); only the token request counts.
			if callErr := tt.call(t.Context(), f); callErr == nil && tt.name != "Head" {
				t.Fatalf("%s = nil, want an error for an object that does not exist", tt.name)
			}

			bodies := tokenBodies(host)

			if len(bodies) != 1 || bodies[0] != tt.want {
				t.Fatalf("%s: the token requests = %v, want one: %s", tt.name, bodies, tt.want)
			}
		})
	}
}

// deletable adds the answers of a branch that nothing protects and that no open
// review request has as its head, to the answers of a delete test.
func deletable(answers map[string]hostAnswer) map[string]hostAnswer {
	answers[branchPath] = okAnswer(map[string]any{"name": "dustpan/j-1", "protected": false})
	answers[listPath] = okAnswer([]any{})

	return answers
}

// TestDeleteBranchGuardsTheBranch checks the read just before the delete: a
// protected branch, a branch with an open review request, and answers that
// cannot be read each stop the delete.
func TestDeleteBranchGuardsTheBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		branch hostAnswer
		list   hostAnswer
		want   error
	}{
		{"a protected branch", okAnswer(map[string]any{"protected": true}), okAnswer([]any{}), forge.ErrRefused},
		{"no protection flag", okAnswer(map[string]any{}), okAnswer([]any{}), forge.ErrUnavailable},
		{"a flag that is text", okAnswer(map[string]any{"protected": "no"}), okAnswer([]any{}), forge.ErrUnavailable},
		{"a missing branch", statusAnswer(404, ""), okAnswer([]any{}), forge.ErrNotFound},
		{"a branch answer that does not parse", statusAnswer(200, "{"), okAnswer([]any{}), forge.ErrUnavailable},
		{
			"an open review request", okAnswer(map[string]any{"protected": false}),
			okAnswer([]any{listItem(7, "dustpan/j-1", "main", 900)}), forge.ErrRefused,
		},
		{
			"a list that does not parse",
			okAnswer(map[string]any{"protected": false}),
			statusAnswer(200, "{"),
			forge.ErrUnavailable,
		},
		{
			"a list that fails",
			okAnswer(map[string]any{"protected": false}),
			statusAnswer(500, ""),
			forge.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{
				repoPath: okAnswer(map[string]any{"default_branch": "main"}), branchPath: tt.branch, listPath: tt.list,
				refPath: {status: http.StatusNoContent, body: "", header: nil, next: nil},
			})

			if err := f.DeleteBranch(t.Context(), "acme/infra", "dustpan/j-1"); !errors.Is(err, tt.want) {
				t.Fatalf("DeleteBranch = %v, want %v", err, tt.want)
			}

			if f.sent(http.MethodDelete) {
				t.Fatalf("DeleteBranch sent a DELETE after the guard refused it; calls %v", f.calls())
			}
		})
	}
}

// TestDeleteBranchRefusesTheBaseOfAnOpenReview checks that a branch that an open
// review request has as its base is not deleted: the delete would close it.
func TestDeleteBranchRefusesTheBaseOfAnOpenReview(t *testing.T) {
	t.Parallel()

	answers := deletable(map[string]hostAnswer{
		repoPath: okAnswer(map[string]any{"default_branch": "main"}),
		refPath:  {status: http.StatusNoContent, body: "", header: nil, next: nil},
		"GET /repos/acme/infra/pulls?state=open&per_page=1&base=dustpan%2Fj-1": okAnswer(
			[]any{listItem(7, "feature/x", "dustpan/j-1", 900)},
		),
	})
	f := cannedForge(t, answers)

	if err := f.DeleteBranch(t.Context(), "acme/infra", "dustpan/j-1"); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("DeleteBranch = %v, want ErrRefused", err)
	}

	if f.sent(http.MethodDelete) {
		t.Fatalf("DeleteBranch sent a DELETE; calls %v", f.calls())
	}
}

// TestCloseReviewChecksOwnershipOfAClosedReview checks that the prefix and the
// fork checks run before the answer for a review request that is closed or
// merged already.
func TestCloseReviewChecksOwnershipOfAClosedReview(t *testing.T) {
	t.Parallel()

	closed := func(p map[string]any) { p["state"] = "closed" }
	merged := func(p map[string]any) {
		p["state"], p["merged"], p["merged_at"] = "closed", true, "2026-01-01T00:00:00Z"
	}

	tests := []struct {
		name string
		edit func(map[string]any)
		want error
	}{
		{
			"a closed branch of a person",
			func(p map[string]any) { closed(p); pullEnd(p, "head")["ref"] = "feature/x" },
			forge.ErrRefused,
		},
		{
			"a closed fork",
			func(p map[string]any) { closed(p); pullEnd(p, "head")["repo"] = map[string]any{"id": 901} },
			forge.ErrRefused,
		},
		{
			"a merged branch of a person",
			func(p map[string]any) { merged(p); pullEnd(p, "head")["ref"] = "feature/x" },
			forge.ErrRefused,
		},
		{"a closed pull request of the write-back", closed, nil},
		{"a merged pull request of the write-back", merged, forge.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{pullPath: okAnswer(pullWith(tt.edit))})

			err := f.CloseReview(t.Context(), "acme/infra", "7")
			if !errors.Is(err, tt.want) && (tt.want != nil || err != nil) {
				t.Fatalf("CloseReview = %v, want %v", err, tt.want)
			}
		})
	}
}

// tokenBodies returns the bodies of the requests for an installation token that
// the fake got.
func tokenBodies(host *githubfake.Fake) []string {
	var bodies []string

	for _, r := range host.Requests() {
		if strings.HasSuffix(r.URI, "/access_tokens") {
			bodies = append(bodies, string(r.Body))
		}
	}

	return bodies
}

// TestMissingPullRequestPermissionIsNamed checks that the error of an
// installation without the permission of the pull requests names the pull
// requests, and not only the contents.
func TestMissingPullRequestPermissionIsNamed(t *testing.T) {
	t.Parallel()

	host := newHostWith(t, func(c *githubfake.Config) {
		c.Permissions = map[string]string{"contents": "write", "metadata": "read"}
	})
	o := newHostOpener(t, host)
	conn, cred := hostConnection(t, host, forge.AuthApp)

	f, err := o.Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	// No Check ran, so the adapter does not know the permissions and calls GitHub.
	_, readErr := f.ReadReview(t.Context(), conformanceRepo, "1")
	if !errors.Is(readErr, forge.ErrAuth) || !strings.Contains(readErr.Error(), "pull requests") {
		t.Fatalf("ReadReview = %v, want ErrAuth that names the pull requests", readErr)
	}

	// The first refusal taught the adapter the permissions, and it lists no review
	// capability now. A second connection has not learned them yet.
	g, err := o.Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	_, openErr := g.OpenReview(t.Context(), conformanceRepo, reviewRequest())
	if !errors.Is(openErr, forge.ErrRefused) || !strings.Contains(openErr.Error(), "pull requests") {
		t.Fatalf("OpenReview = %v, want ErrRefused that names the pull requests", openErr)
	}
}

// TestRefusedCallEvictsTheCachedToken checks that the adapter drops the cached
// installation token of a level after GitHub refused it, so that the next call
// asks for a new token.
func TestRefusedCallEvictsTheCachedToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(ctx context.Context, f forge.Forge) error
	}{
		{"ReadReview", func(ctx context.Context, f forge.Forge) error {
			return second(f.ReadReview(ctx, conformanceRepo, "1"))
		}},
		{"CloseReview", func(ctx context.Context, f forge.Forge) error {
			return f.CloseReview(ctx, conformanceRepo, "1")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := newHost(t)
			o := newHostOpener(t, host)
			conn, cred := hostConnection(t, host, forge.AuthApp)

			f, err := o.Open(t.Context(), conn, cred)
			if err != nil {
				t.Fatal(err)
			}

			wantCall(t, tt.name, tt.call(t.Context(), f), forge.ErrNotFound)

			restore := host.Refuse()
			wantCall(t, tt.name, tt.call(t.Context(), f), forge.ErrAuth)
			restore()

			wantCall(t, tt.name, tt.call(t.Context(), f), forge.ErrNotFound)

			if got := len(tokenBodies(host)); got != 2 {
				t.Fatalf("%s asked for %d installation tokens, want 2 (a new one after the refusal)", tt.name, got)
			}
		})
	}
}

// wantCall fails the test unless err is want.
func wantCall(t *testing.T, name string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", name, err, want)
	}
}
