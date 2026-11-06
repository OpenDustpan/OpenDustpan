package githubfake_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// openPull opens the pull request 1 from a new branch and returns it.
func openPull(t *testing.T, c *client) {
	t.Helper()

	mustPush(t, c.f, "dustpan/j-1", map[string][]byte{"a.txt": []byte("a")})
	wantStatus(t, c.do(http.MethodPost, "/repos/acme/infra/pulls", userValue,
		`{"title":"t","body":"b","head":"dustpan/j-1","base":"main"}`), http.StatusCreated)
}

type commentAnswer struct {
	ID   int            `json:"id"`
	Body string         `json:"body"`
	User map[string]any `json:"user"`
}

func TestCommentLifecycle(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	openPull(t, c)

	a := c.do(http.MethodPost, "/repos/acme/infra/issues/1/comments", userValue, `{"body":"first"}`)
	wantStatus(t, a, http.StatusCreated)

	made := decode[commentAnswer](t, a.body)
	if made.Body != "first" || made.ID == 0 || made.User["login"] != "acme" {
		t.Fatalf("the new comment = %+v", made)
	}

	urls := decode[map[string]any](t, a.body)
	if !strings.HasSuffix(fmt.Sprint(urls["issue_url"]), "/repos/acme/infra/issues/1") ||
		!strings.HasPrefix(fmt.Sprint(urls["html_url"]), "https://") {
		t.Fatalf("the addresses of the new comment = %v and %v", urls["issue_url"], urls["html_url"])
	}

	path := fmt.Sprintf("/repos/acme/infra/issues/comments/%d", made.ID)

	wantStatus(t, c.do(http.MethodPatch, path, userValue, `{"body":"second"}`), http.StatusOK)

	read := c.do(http.MethodGet, path, userValue, "")
	wantStatus(t, read, http.StatusOK)

	if got := decode[commentAnswer](t, read.body); got.Body != "second" || got.ID != made.ID {
		t.Fatalf("the comment after the edit = %+v", got)
	}

	list := c.do(http.MethodGet, "/repos/acme/infra/issues/1/comments", userValue, "")
	wantStatus(t, list, http.StatusOK)

	if got := decode[[]commentAnswer](t, list.body); len(got) != 1 || got[0].Body != "second" {
		t.Fatalf("the list = %+v", got)
	}
}

func TestCommentRefusals(t *testing.T) {
	t.Parallel()

	f, _ := newFakeWith(t, map[string]string{"contents": "read", "metadata": "read", "issues": "write"}, false)
	c := newClient(t, f)
	openPull(t, c)

	issuesWrite := installationToken(t, c, `{"permissions":{"issues":"write"}}`)
	issuesRead := installationToken(t, c, `{"permissions":{"issues":"read"}}`)
	contentsOnly := installationToken(t, c, `{"permissions":{"contents":"read"}}`)
	other, err := f.AddComment("acme/infra", 1, "other-dev", "from a person")
	if err != nil {
		t.Fatal(err)
	}

	otherPath := fmt.Sprintf("/repos/acme/infra/issues/comments/%d", other.ID)
	long := `{"body":"` + strings.Repeat("é", 65537) + `"}`
	atLimit := `{"body":"` + strings.Repeat("é", 65536) + `"}`

	tests := []struct {
		name   string
		method string
		path   string
		bearer string
		body   string
		status int
	}{
		{
			"create with the permission to write",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			issuesWrite,
			`{"body":"x"}`,
			http.StatusCreated,
		},
		{
			"create with the permission to read",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			issuesRead,
			`{"body":"x"}`,
			http.StatusForbidden,
		},
		{
			"create with the permission for contents only",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			contentsOnly,
			`{"body":"x"}`,
			http.StatusForbidden,
		},
		{
			"read with the permission to read",
			http.MethodGet,
			"/repos/acme/infra/issues/1/comments",
			issuesRead,
			"",
			http.StatusOK,
		},
		{
			"read with the permission for contents only",
			http.MethodGet,
			"/repos/acme/infra/issues/1/comments",
			contentsOnly,
			"",
			http.StatusForbidden,
		},
		{
			"no credential",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			"",
			`{"body":"x"}`,
			http.StatusUnauthorized,
		},
		{
			"a missing pull request",
			http.MethodPost,
			"/repos/acme/infra/issues/99/comments",
			userValue,
			`{"body":"x"}`,
			http.StatusNotFound,
		},
		{
			"a number that is text",
			http.MethodGet,
			"/repos/acme/infra/issues/abc/comments",
			userValue,
			"",
			http.StatusNotFound,
		},
		{
			"no text",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			userValue,
			`{}`,
			http.StatusUnprocessableEntity,
		},
		{
			"a text above the limit",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			userValue,
			long,
			http.StatusUnprocessableEntity,
		},
		{
			"a text at the limit",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			userValue,
			atLimit,
			http.StatusCreated,
		},
		{
			"a body that is not JSON",
			http.MethodPost,
			"/repos/acme/infra/issues/1/comments",
			userValue,
			`{`,
			http.StatusBadRequest,
		},
		{
			"an edit of a missing comment",
			http.MethodPatch,
			"/repos/acme/infra/issues/comments/1",
			userValue,
			`{"body":"x"}`,
			http.StatusNotFound,
		},
		{
			"an edit of a comment of a person by the app",
			http.MethodPatch,
			otherPath,
			issuesWrite,
			`{"body":"x"}`,
			http.StatusForbidden,
		},
		{
			"a read of a missing comment",
			http.MethodGet,
			"/repos/acme/infra/issues/comments/1",
			userValue,
			"",
			http.StatusNotFound,
		},
		{
			"a path with a third part",
			http.MethodGet,
			"/repos/acme/infra/issues/1/other",
			userValue,
			"",
			http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		if a := c.do(tt.method, tt.path, tt.bearer, tt.body); a.status != tt.status {
			t.Errorf("%s: status = %d, want %d (body %.80s)", tt.name, a.status, tt.status, a.body)
		}
	}

	// The comment of the person did not change.
	shown, err := f.Comments("acme/infra", 1)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range shown {
		if s.ID == other.ID && s.Body != "from a person" {
			t.Fatalf("the comment of a person changed to %q", s.Body)
		}
	}
}

func TestCommentPages(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	openPull(t, c)

	for i := range 5 {
		if _, err := f.AddComment("acme/infra", 1, "other-dev", fmt.Sprintf("c%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	page := func(n int) []string {
		a := c.do(
			http.MethodGet,
			fmt.Sprintf("/repos/acme/infra/issues/1/comments?per_page=2&page=%d", n),
			userValue,
			"",
		)
		wantStatus(t, a, http.StatusOK)

		bodies := make([]string, 0, 2)
		for _, x := range decode[[]commentAnswer](t, a.body) {
			bodies = append(bodies, x.Body)
		}

		return bodies
	}

	if got := page(1); !slices.Equal(got, []string{"c0", "c1"}) {
		t.Fatalf("page 1 = %v", got)
	}

	if got := page(3); !slices.Equal(got, []string{"c4"}) {
		t.Fatalf("page 3 = %v", got)
	}

	if got := page(4); len(got) != 0 {
		t.Fatalf("page 4 = %v, want none", got)
	}
}

func TestReactions(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	openPull(t, c)

	made, err := f.AddComment("acme/infra", 1, "other-dev", "text")
	if err != nil {
		t.Fatal(err)
	}

	path := fmt.Sprintf("/repos/acme/infra/issues/comments/%d/reactions", made.ID)

	// 201 for a new reaction and 200 for one that exists.
	wantStatus(t, c.do(http.MethodPost, path, userValue, `{"content":"+1"}`), http.StatusCreated)
	wantStatus(t, c.do(http.MethodPost, path, userValue, `{"content":"+1"}`), http.StatusOK)
	wantStatus(t, c.do(http.MethodPost, path, userValue, `{"content":"eyes"}`), http.StatusCreated)
	wantStatus(t, c.do(http.MethodPost, path, userValue, `{"content":"thumbsup"}`), http.StatusUnprocessableEntity)
	wantStatus(t, c.do(http.MethodPost, path, userValue, `{`), http.StatusBadRequest)
	wantStatus(t, c.do(http.MethodPost, "/repos/acme/infra/issues/comments/1/reactions", userValue, `{"content":"+1"}`),
		http.StatusNotFound)
	wantStatus(t, c.do(http.MethodPost, path, "", `{"content":"+1"}`), http.StatusUnauthorized)

	got, err := f.Reactions("acme/infra", made.ID)
	if err != nil || !slices.Equal(got, []string{"+1:acme", "eyes:acme"}) {
		t.Fatalf("Reactions = %v, %v; want one +1 and one eyes of acme", got, err)
	}
}

func TestContentsDirectoryHasTreeIDs(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	a := c.do(http.MethodGet, "/repos/acme/infra/contents/?ref=main", userValue, "")
	wantStatus(t, a, http.StatusOK)

	for _, e := range decode[[]map[string]any](t, a.body) {
		if e["type"] == "dir" && len(fmt.Sprint(e["sha"])) != 40 {
			t.Fatalf("the directory %v has the object ID %q, want a tree ID", e["name"], e["sha"])
		}
	}
}

func TestAccountByLogin(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	tests := []struct {
		name   string
		path   string
		bearer string
		status int
		login  string
	}{
		{"the user of the token", "/users/acme", userValue, http.StatusOK, "acme"},
		{"the bot of the app", "/users/" + f.AppLogin(), userValue, http.StatusOK, f.AppLogin()},
		{"a person of AddComment", "/users/other-dev", userValue, http.StatusOK, "other-dev"},
		{"a login that is not known", "/users/nobody", userValue, http.StatusNotFound, ""},
		{"no credential", "/users/acme", "", http.StatusUnauthorized, ""},
	}

	for _, tt := range tests {
		a := c.do(http.MethodGet, tt.path, tt.bearer, "")
		if a.status != tt.status {
			t.Errorf("%s: status = %d, want %d", tt.name, a.status, tt.status)

			continue
		}

		if tt.login != "" {
			if got := decode[map[string]any](t, a.body); got["login"] != tt.login || got["id"] == nil {
				t.Errorf("%s: the account = %v", tt.name, got)
			}
		}
	}
}
