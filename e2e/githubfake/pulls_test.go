package githubfake_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e/githubfake"
)

// pullDoc is the JSON answer for a pull request, read by key path.
type pullDoc map[string]any

func readPull(t *testing.T, a answer) pullDoc {
	t.Helper()

	var p pullDoc
	if err := json.Unmarshal(a.body, &p); err != nil {
		t.Fatalf("the body %q is not a pull request: %v", a.body, err)
	}

	return p
}

// isTrue reports whether v is the JSON value true.
func isTrue(v any) bool {
	b, isBool := v.(bool)

	return isBool && b
}

// at returns the value at the key path, or nil.
func (p pullDoc) at(keys ...string) any {
	var cur any = map[string]any(p)

	for _, k := range keys {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return nil
		}

		cur = m[k]
	}

	return cur
}

func TestPullLifecycle(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	head := mustPush(t, f, "dustpan/j-1", map[string][]byte{"a.txt": []byte("a")})

	open := c.do(http.MethodPost, "/repos/acme/infra/pulls", userValue,
		`{"title":"t","body":"b","head":"dustpan/j-1","base":"main"}`)
	if open.status != http.StatusCreated {
		t.Fatalf("POST pulls = %d %s", open.status, open.body)
	}

	p := readPull(t, open)
	if p.at("number") != float64(1) || p.at("state") != "open" || p.at("head", "sha") != head.ID ||
		p.at("head", "repo", "id") != p.at("base", "repo", "id") || p.at("user", "id") != float64(777) {
		t.Fatalf("the new pull request = %v", p)
	}

	if p.at("mergeable_state") != "clean" || !isTrue(p.at("mergeable")) {
		t.Fatalf("the merge state of the new pull request = %v %v", p.at("mergeable_state"), p.at("mergeable"))
	}

	if sha, isText := p.at("merge_commit_sha").(string); !isText || len(sha) != 40 {
		t.Fatalf("the test merge commit = %v, want a commit ID", p.at("merge_commit_sha"))
	}
}

func TestPullListAndClose(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	mustPush(t, f, "dustpan/j-1", map[string][]byte{"a.txt": []byte("a")})
	c.do(http.MethodPost, "/repos/acme/infra/pulls", userValue, `{"title":"t","head":"dustpan/j-1","base":"main"}`)

	again := c.do(http.MethodPost, "/repos/acme/infra/pulls", userValue,
		`{"title":"t","head":"dustpan/j-1","base":"main"}`)
	if again.status != http.StatusUnprocessableEntity || !strings.Contains(string(again.body), "already exists") {
		t.Fatalf("a second POST pulls = %d %s, want 422 already exists", again.status, again.body)
	}

	list := c.do(http.MethodGet, "/repos/acme/infra/pulls?state=open&head=acme:dustpan/j-1&base=main", userValue, "")
	if list.status != http.StatusOK || !strings.Contains(string(list.body), `"number":1`) {
		t.Fatalf("GET pulls = %d %s", list.status, list.body)
	}

	other := c.do(http.MethodGet, "/repos/acme/infra/pulls?head=acme:other", userValue, "")
	if strings.TrimSpace(string(other.body)) != "[]" {
		t.Fatalf("GET pulls of another head = %s, want []", other.body)
	}

	closed := c.do(http.MethodPatch, "/repos/acme/infra/pulls/1", userValue, `{"state":"closed"}`)
	if got := readPull(t, closed); closed.status != http.StatusOK || got.at("state") != "closed" ||
		isTrue(got.at("merged")) {
		t.Fatalf("PATCH pulls = %d %v", closed.status, got)
	}

	missing := c.do(http.MethodGet, "/repos/acme/infra/pulls/9", userValue, "")
	if missing.status != http.StatusNotFound {
		t.Fatalf("GET of a missing pull request = %d, want 404", missing.status)
	}
}

func TestPullCreateRefusals(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	mustPush(t, f, "dustpan/j-1", map[string][]byte{"a.txt": []byte("a")})

	if err := f.Branch("acme/infra", "dustpan/same"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{"a missing head", `{"title":"t","head":"nope","base":"main"}`, `"field":"head"`},
		{"a missing base", `{"title":"t","head":"dustpan/j-1","base":"nope"}`, `"field":"base"`},
		{"an empty title", `{"title":"","head":"dustpan/j-1","base":"main"}`, `"field":"title"`},
		{"a head with no new commit", `{"title":"t","head":"dustpan/same","base":"main"}`, "No commits between"},
	}

	for _, tt := range tests {
		a := c.do(http.MethodPost, "/repos/acme/infra/pulls", userValue, tt.body)
		if a.status != http.StatusUnprocessableEntity || !strings.Contains(string(a.body), tt.want) {
			t.Errorf("%s: POST pulls = %d %s, want 422 with %s", tt.name, a.status, a.body, tt.want)
		}
	}
}

func TestPullFromFork(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	if err := f.AddFork("acme/infra", "forker/infra"); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{"f.txt": []byte("f")}
	if _, err := f.Push("forker/infra", "dustpan/j-1", files, "Fork change"); err != nil {
		t.Fatal(err)
	}

	n, err := f.OpenPull(githubfake.PullSpec{
		Base: "acme/infra", BaseRef: "main", Head: "forker/infra", HeadRef: "dustpan/j-1", Title: "t", Body: "",
		Author: githubfake.Account{ID: 31337, Login: "forker"},
	})
	if err != nil {
		t.Fatal(err)
	}

	p := readPull(t, c.do(http.MethodGet, "/repos/acme/infra/pulls/1", userValue, ""))
	if n != 1 || p.at("head", "repo", "id") == p.at("base", "repo", "id") || p.at("head", "repo", "id") == nil ||
		p.at("user", "id") != float64(31337) {
		t.Fatalf("the pull request of a fork = %d %v, want another head repository ID", n, p)
	}
}

func TestPullMergeAndMergeState(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	mustPush(t, f, "dustpan/j-1", map[string][]byte{"a.txt": []byte("a")})
	c.do(http.MethodPost, "/repos/acme/infra/pulls", userValue, `{"title":"t","head":"dustpan/j-1","base":"main"}`)

	if err := f.SetMergeState("acme/infra", 1, "unknown"); err != nil {
		t.Fatal(err)
	}

	unknown := readPull(t, c.do(http.MethodGet, "/repos/acme/infra/pulls/1", userValue, ""))
	if unknown.at("mergeable") != nil || unknown.at("mergeable_state") != "unknown" ||
		unknown.at("merge_commit_sha") != nil {
		t.Fatalf("the pull request with an unknown merge state = %v", unknown)
	}

	if err := f.MergePull("acme/infra", 1); err != nil {
		t.Fatal(err)
	}

	merged := readPull(t, c.do(http.MethodGet, "/repos/acme/infra/pulls/1", userValue, ""))
	if sha, isText := merged.at("merge_commit_sha").(string); merged.at("state") != "closed" ||
		!isTrue(merged.at("merged")) || !isText || len(sha) != 40 {
		t.Fatalf("the merged pull request = %v", merged)
	}

	patch := c.do(http.MethodPatch, "/repos/acme/infra/pulls/1", userValue, `{"state":"closed"}`)
	if patch.status != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH of a merged pull request = %d, want 422", patch.status)
	}
}

func TestPullNeedsThePermission(t *testing.T) {
	t.Parallel()

	f, _ := newFakeWith(t, map[string]string{"contents": "write", "metadata": "read"}, false)
	c := newClient(t, f)
	token := installationToken(t, c, `{"repositories":["infra"],"permissions":{"contents":"write"}}`)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		a := c.do(method, "/repos/acme/infra/pulls", token, `{"title":"t","head":"x","base":"main"}`)
		if a.status != http.StatusForbidden {
			t.Errorf("%s pulls with no pull request permission = %d, want 403", method, a.status)
		}
	}
}

func TestDeleteRefAndRepository(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	mustPush(t, f, "dustpan/j-1", map[string][]byte{"a.txt": []byte("a")})
	mustPush(t, f, "dustpan/p", map[string][]byte{"p.txt": []byte("p")})

	if err := f.Protect("acme/infra", "dustpan/p"); err != nil {
		t.Fatal(err)
	}

	if err := f.SetDefaultBranch("acme/infra", "dustpan/j-1"); err != nil {
		t.Fatal(err)
	}

	branch := c.do(http.MethodGet, "/repos/acme/infra/branches/dustpan/p", userValue, "")
	if branch.status != http.StatusOK || !strings.Contains(string(branch.body), `"protected":true`) {
		t.Fatalf("GET branch = %d %s, want a protected branch", branch.status, branch.body)
	}

	none := c.do(http.MethodGet, "/repos/acme/infra/branches/none", userValue, "")
	if none.status != http.StatusNotFound {
		t.Fatalf("GET of a missing branch = %d, want 404", none.status)
	}

	repo := c.do(http.MethodGet, "/repos/acme/infra", userValue, "")
	if repo.status != http.StatusOK || !strings.Contains(string(repo.body), `"default_branch":"dustpan/j-1"`) {
		t.Fatalf("GET repository = %d %s", repo.status, repo.body)
	}

	tests := []struct {
		ref    string
		status int
	}{
		{"dustpan/p", http.StatusUnprocessableEntity},
		{"dustpan/none", http.StatusUnprocessableEntity},
		{"dustpan/j-1", http.StatusNoContent},
	}

	for _, tt := range tests {
		a := c.do(http.MethodDelete, "/repos/acme/infra/git/refs/heads/"+tt.ref, userValue, "")
		if a.status != tt.status {
			t.Errorf("DELETE %s = %d %s, want %d", tt.ref, a.status, a.body, tt.status)
		}
	}
}
